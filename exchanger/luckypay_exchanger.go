package exchanger

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"payment-service-go/models"
	"strconv"
	"time"
)

type LuckyPayExchanger struct {
	config    models.Exchanger
	processor *Processor
}

func NewLuckyPayExchanger(config models.Exchanger, processor *Processor) *LuckyPayExchanger {
	return &LuckyPayExchanger{config: config, processor: processor}
}

func (l *LuckyPayExchanger) CheckInvoices(invoices []models.InvoiceCheckLite, serviceID uint64) error {
	var externalIDs []int64

	for _, inv := range invoices {
		id, err := strconv.ParseInt(inv.ExternalID, 10, 64)
		if err == nil {
			externalIDs = append(externalIDs, id)
		}
	}

	reqBody, err := json.Marshal(map[string]interface{}{
		"page":         1,
		"size":         100,
		"order_side":   "buy",
		"order_status": "Completed,CanceledByTimeout,CanceledByService",
	})

	if err != nil {
		return err
	}

	req, err := http.NewRequest("GET", l.config.Endpoint+"/api/v/1/order", bytes.NewBuffer(reqBody))
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-API-Key", l.config.APIKey)

	client := &http.Client{Timeout: 8 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	if resp.StatusCode != 200 && resp.StatusCode != 201 {
		return errors.New("[LuckyPay] сервер вернул ошибку: " + string(body))
	}

	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		return err
	}

	success, ok := result["success"].(bool)
	if !ok || !success {
		msg, _ := result["message"].(string)
		return fmt.Errorf("[LuckyPay] сервер вернул ошибку: %v", msg)
	}

	orders, ok := result["orders"].(map[string]interface{})
	if !ok {
		return errors.New("[LuckyPay] не удалось получить 'orders'")
	}

	ordersItems, ok := orders["items"].([]interface{})
	if !ok {
		return errors.New("[LuckyPay] не удалось получить 'items'")
	}
	processor := NewProcessor()

	for _, orderItem := range ordersItems {
		orderItemData, ok := orderItem.(map[string]interface{})
		if !ok {
			log.Println("[LuckyPay] не удалось получить информацию о orderItemData")
		}

		id, ok := orderItemData["id"].(string)
		if !ok {
			log.Println("[LuckyPay]Не удалось получить 'id'")
		}

		status, ok := orderItemData["status"].(string)
		if !ok {
			log.Println("[LuckyPay] не удалось получить 'status'")
		}

		invoice, err := processor.MysqlLogger.GetInvoiceByExternalIDAndServiceID(id, serviceID)
		if err != nil || invoice == nil {
			log.Println("[LuckyPay] не удалось получить счет по ExternalID: %v", id)
			continue
		}

		err = l.processStatusInvoice(processor, *invoice, status)
		if err != nil {
			log.Println("[LuckyPay] не удалось обработать статус счета InvoiceID: %v", invoice.ID)
			continue
		}
	}

	return nil
}

func (l *LuckyPayExchanger) processStatusInvoice(processor *Processor, invoice models.InvoiceCheckLite, orderStatus string) error {
	switch orderStatus {
	case "Completed":
		err := processor.MysqlLogger.UpdateInvoiceStatus(invoice, "paid")
		if err != nil {
			return err
		}
	case "CanceledByTimeout":
		err := processor.MysqlLogger.UpdateInvoiceStatus(invoice, "cancel_time")
		if err != nil {
			return err
		}
	case "CanceledByService":
		err := processor.MysqlLogger.UpdateInvoiceStatus(invoice, "cancel_operator")
		if err != nil {
			return err
		}
	default:
		return errors.New("Не получилось обработать статус")
	}
	return nil
}

func (l *LuckyPayExchanger) GetRequisites(task models.InvoiceTask, ex models.Exchanger) (models.DetailsRequisites, error) {
	client := &http.Client{Timeout: 8 * time.Second}

	// Шаблон тела
	bodyMap := map[string]interface{}{
		"client_order_id":          fmt.Sprintf("%d", task.Invoice.ID),
		"order_side":               "Buy",
		"payment_method_id":        "8fe3669a-a448-4053-bc4b-43bb51cb3e9d", // банковская карта
		"amount":                   strconv.FormatFloat(ex.Amount, 'f', -1, 64),
		"customer_payment_account": nil,
	}

	tryRequest := func() (*http.Response, []byte, error) {
		reqBody, err := json.Marshal(bodyMap)
		if err != nil {
			return nil, nil, err
		}

		urlApi := ex.Endpoint + "/api/v1/order/"

		req, err := http.NewRequest("POST", urlApi, bytes.NewBuffer(reqBody))
		if err != nil {
			return nil, nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
		req.Header.Set("X-API-Key", ex.APIKey)

		resp, err := client.Do(req)
		if err != nil {
			return nil, nil, err
		}
		defer resp.Body.Close()

		body, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			return nil, nil, err
		}

		if resp.StatusCode != 200 && resp.StatusCode != 201 {
			return nil, body, errors.New("сервер вернул ошибку")
		}

		l.processor.ClickLogger.ApiRequests(urlApi, resp.StatusCode, string(body), string(reqBody), task.Invoice.ID, ex.ID)

		return resp, body, nil
	}

	// Первый запрос — банковская карта
	_, body, err := tryRequest()
	if err != nil {
		// Второй запрос — СБП
		bodyMap["payment_method_id"] = "2ec6dbd6-49a5-45d0-bd6d-b0134ee4639a"
		_, body, err = tryRequest()
		if err != nil {
			return models.DetailsRequisites{}, fmt.Errorf("оба метода оплаты не сработали: %v (%s)", err, string(body))
		}
	}

	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		return models.DetailsRequisites{}, err
	}

	return l.ReturnFormattedDetails(result)
}

func (l *LuckyPayExchanger) ReturnFormattedDetails(data map[string]interface{}) (models.DetailsRequisites, error) {
	idRaw, ok := data["id"]
	if !ok || idRaw == nil {
		return models.DetailsRequisites{}, errors.New("'id' is empty")
	}

	id, ok := idRaw.(string)
	if !ok {
		return models.DetailsRequisites{}, errors.New("'id' is empty and not a string")
	}

	requisites, ok := data["holder_account"].(string)
	if !ok {
		return models.DetailsRequisites{}, errors.New("'requisites' is empty")
	}

	untilAt, ok := data["expires_at"].(string)
	if !ok {
		return models.DetailsRequisites{}, errors.New("'expires_at' is empty")
	}

	amountIn, ok := data["amount"].(float64)
	if !ok {
		return models.DetailsRequisites{}, errors.New("'amount_payable' не число")
	}

	externalMethodName := data["method_name"].(string)
	externalHolderName := data["holder_name"].(string)

	important := make(map[string]interface{})
	data["important"] = important

	important["external_method_name"] = externalMethodName
	important["external_holder_name"] = externalHolderName

	return models.DetailsRequisites{
		ID:         id,
		AmountIn:   amountIn,
		UntilAt:    untilAt,
		Requisites: requisites,
		Details:    data,
	}, nil

}
