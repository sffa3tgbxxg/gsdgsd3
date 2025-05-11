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

type GreengoExchanger struct {
	config    models.Exchanger
	processor *Processor
}

func NewGreengoExchanger(config models.Exchanger, processor *Processor) *GreengoExchanger {
	return &GreengoExchanger{config: config, processor: processor}
}

func (g *GreengoExchanger) CheckInvoices(invoices []models.InvoiceCheckLite, serviceID uint64) error {
	var externalIDs []int64

	for _, inv := range invoices {
		id, err := strconv.ParseInt(inv.ExternalID, 10, 64)
		if err == nil {
			externalIDs = append(externalIDs, id)
		}
	}

	reqBody, err := json.Marshal(map[string]interface{}{
		"order_id": externalIDs,
	})

	if err != nil {
		return err
	}

	req, err := http.NewRequest("POST", g.config.Endpoint+"/api/v2/order/check/", bytes.NewBuffer(reqBody))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Api-Secret", g.config.APIKey)

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
		return errors.New("[Greengo] сервер вернул ошибку: " + string(body))
	}

	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		return err
	}

	data, ok := result["data"].(map[string]interface{})
	if !ok {
		return errors.New("[Greengo] 'data' пустая")
	}

	orders, ok := data["orders"].([]interface{})
	if !ok || len(orders) <= 0 {
		return errors.New("[Greengo] в 'orders' пусто")
	}

	for _, order := range orders {
		orderData, ok := order.(map[string]interface{})
		if !ok {
			log.Printf("[Greengo] не удалось прочитать 'orders'")
			continue
		}

		externalOrderId, ok := orderData["order_id"].(float64)
		if !ok {
			log.Printf("[Greengo] не удалось получить 'order_id'")
			continue
		}

		externalOrderIdINT := uint64(externalOrderId)

		statusOrder, ok := orderData["order_status"].(string)
		if !ok {
			log.Printf("[Greengo] не удалось получить 'order_status'")
		}

		invoiceByExternalID, err := g.processor.MysqlLogger.GetInvoiceByExternalIDAndServiceID(fmt.Sprintf("%v", externalOrderIdINT), serviceID)

		if invoiceByExternalID == nil || err != nil {
			log.Printf("[Greengo] не удалось получить счет по External ID")
			continue
		}

		err = g.processedOrderStatus(*invoiceByExternalID, statusOrder)
		if err != nil {
			log.Printf("[Greengo] не удалось обработать статус у ExternalOrderID: %v, error: %v", externalOrderId, err)
			continue
		}

	}

	return nil
}

func (g *GreengoExchanger) processedOrderStatus(invoice models.InvoiceCheckLite, orderStatus string) error {
	switch orderStatus {
	case "payed":
		err := g.processor.MysqlLogger.UpdateInvoiceStatus(invoice, "pending_confirm")
		if err != nil {
			return err
		}
	case "completed":
		err := g.processor.MysqlLogger.UpdateInvoiceStatus(invoice, "paid")
		if err != nil {
			return err
		}
	case "unconfirmed":
		return nil
	case "awaiting":
		return nil
	case "autocanceled":
		err := g.processor.MysqlLogger.UpdateInvoiceStatus(invoice, "cancel_time")
		if err != nil {
			return err
		}
	default:
		return errors.New("Не получилось обработать статус")
	}
	return nil
}

func (g *GreengoExchanger) GetRequisites(task models.InvoiceTask, ex models.Exchanger) (models.DetailsRequisites, error) {
	reqBody, err := json.Marshal(map[string]interface{}{
		"payment_method": "card",
		"wallet":         "xxxxxxxxxxx",
		"from_amount":    ex.Amount,
	})

	if err != nil {
		return models.DetailsRequisites{}, err
	}

	req, err := http.NewRequest("POST", ex.Endpoint+"/api/v2/order/create", bytes.NewBuffer(reqBody))
	if err != nil {
		return models.DetailsRequisites{}, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Api-Secret", ex.APIKey)

	client := &http.Client{Timeout: 8 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return models.DetailsRequisites{}, err
	}
	defer resp.Body.Close()

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return models.DetailsRequisites{}, err
	}

	if resp.StatusCode != 200 && resp.StatusCode != 201 {
		return models.DetailsRequisites{}, errors.New("сервер вернул ошибку: " + string(body))
	}

	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		return models.DetailsRequisites{}, err
	}

	if result["response"] != "success" {
		return models.DetailsRequisites{}, errors.New(result["response"].(string))
	}

	// Проверка, что items — слайс и не пустой
	itemsRaw, ok := result["items"].([]interface{})
	if !ok || len(itemsRaw) == 0 {
		return models.DetailsRequisites{}, errors.New("пустой или некорректный items")
	}

	order, ok := itemsRaw[0].(map[string]interface{})
	if !ok {
		return models.DetailsRequisites{}, errors.New("items[0] не является объектом")
	}

	return g.ReturnFormattedDetails(order)
}

func (g *GreengoExchanger) ReturnFormattedDetails(data map[string]interface{}) (models.DetailsRequisites, error) {
	id, ok := data["order_id"].(string)
	if !ok {
		return models.DetailsRequisites{}, errors.New("'id' is empty")
	}

	requisites, ok := data["wallet_payment"].(string)
	if !ok {
		return models.DetailsRequisites{}, errors.New("'wallet_payment' не строка")
	}

	amountStr, ok := data["amount_payable"].(string)
	if !ok {
		return models.DetailsRequisites{}, errors.New("'amount_payable' не строка")
	}

	amountIn, err := strconv.ParseFloat(amountStr, 64)
	if err != nil {
		return models.DetailsRequisites{}, errors.New("не удалось преобразовать 'amount_payable' в число: " + err.Error())
	}

	nowUTC := time.Now().UTC()
	untilAt := (nowUTC.Add(20 * time.Minute)).Format("2006-01-02 15:04:05")

	return models.DetailsRequisites{
		ID:         id,
		AmountIn:   amountIn,
		UntilAt:    untilAt,
		Requisites: requisites,
		Details:    data,
	}, nil

}
