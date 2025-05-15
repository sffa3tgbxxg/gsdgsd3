package exchanger

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha512"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"payment-service-go/models"
	"strconv"
	"time"
)

type RacksExchanger struct {
	config    models.Exchanger
	processor Processor
}

func NewRacksExchanger(config models.Exchanger, processor *Processor) *RacksExchanger {
	return &RacksExchanger{config: config, processor: *processor}
}

func (r *RacksExchanger) CheckInvoices(invoices []models.InvoiceCheckLite, serviceID uint64) error {
	client := &http.Client{Timeout: 8 * time.Second}

	bodyMap := map[string]interface{}{
		"action": "details",
	}

	tryRequest := func() (*http.Response, []byte, error) {
		reqBody, err := json.Marshal(bodyMap)
		if err != nil {
			return nil, nil, err
		}

		req, err := http.NewRequest("POST", r.config.Endpoint+"/api/v1/order/", bytes.NewBuffer(reqBody))
		if err != nil {
			return nil, nil, err
		}

		h := hmac.New(sha512.New, []byte(r.config.SecretKey))
		h.Write([]byte(reqBody))
		signature := fmt.Sprintf("%x", h.Sum(nil))

		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
		req.Header.Set("X-APIKEY", r.config.APIKey)
		req.Header.Set("X-SIGNATURE", signature)

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

		return resp, body, nil
	}

	processor := NewProcessor()

	for _, invoice := range invoices {
		bodyMap["uniqueid"] = invoice.ID
		_, body, err := tryRequest()
		if err != nil {
			fmt.Errorf("[Bitloga] не удалось проверить счет InvoiceID: %v, error: %v", invoice.ID, err)
			continue
		}
		var result map[string]interface{}
		if err := json.Unmarshal(body, &result); err != nil {
			fmt.Errorf("[Bitloga] не удалось проверить счет InvoiceID: %v, error: %v", invoice.ID, err)
			continue
		}

		status, ok := result["status"].(string)
		if !ok {
			fmt.Errorf("[Bitloga] не удалось получить статус у InvoiceID: %v, error: %v", invoice.ID, err)
			continue
		}

		err = r.processStatusInvoice(processor, invoice, status)

		if err != nil {
			fmt.Errorf("[Bitloga] не удалось обрабатотать статус у InvoiceID: %v, error: %v", invoice.ID, err)
			continue
		}
	}

	return nil
}

func (r *RacksExchanger) processStatusInvoice(processor *Processor, invoice models.InvoiceCheckLite, orderStatus string) error {
	switch orderStatus {
	case "Payed":
		err := processor.MysqlLogger.UpdateInvoiceStatus(invoice, "paid")
		if err != nil {
			return err
		}
	case "Pending":
		return nil
	case "Error":
		err := processor.MysqlLogger.UpdateInvoiceStatus(invoice, "error")
		if err != nil {
			return err
		}
	case "Canceled":
		err := processor.MysqlLogger.UpdateInvoiceStatus(invoice, "cancel_time")
		if err != nil {
			return err
		}
	default:
		return errors.New("Не получилось обработать статус")
	}
	return nil
}

func (r *RacksExchanger) GetRequisites(task models.InvoiceTask, ex models.Exchanger) (models.DetailsRequisites, error) {

	data := url.Values{}
	data.Set("amount", strconv.FormatFloat(ex.Amount, 'f', -1, 64))
	data.Set("typecommission", "1")
	data.Set("private_key", ex.SecretKey)
	data.Set("currency", "RUB")

	encoded := data.Encode()

	urlApi := ex.Endpoint + "/fiat_api?" + encoded
	req, err := http.NewRequest("GET", urlApi, nil)
	if err != nil {
		return models.DetailsRequisites{}, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+ex.APIKey)

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

	r.processor.ClickLogger.ApiRequests(urlApi, resp.StatusCode, string(body), encoded, task.Invoice.ID, ex.ID)

	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		return models.DetailsRequisites{}, err
	}
	log.Printf("result: %v", result)

	return r.ReturnFormattedDetails(result)
}

func (r *RacksExchanger) ReturnFormattedDetails(data map[string]interface{}) (models.DetailsRequisites, error) {
	msg, ok := data["msg_error"].(string)
	if !ok || msg != "" {
		if msg != "" {
			return models.DetailsRequisites{}, fmt.Errorf("'msg_error' is not empty. Error: %v", msg)
		} else {
			return models.DetailsRequisites{}, fmt.Errorf("не удалось получить 'msg_error'")
		}
	}

	itemsRaw, ok := data["order"].([]interface{})
	if !ok || len(itemsRaw) == 0 {
		return models.DetailsRequisites{}, fmt.Errorf("не удалось получить 'order'")
	}

	order, ok := itemsRaw[0].(map[string]interface{})
	if !ok {
		return models.DetailsRequisites{}, errors.New("'order' не является объектом")
	}

	externalOrderID, ok := order["id"].(string)
	if !ok {
		return models.DetailsRequisites{}, errors.New("не удалось получить 'ID'")
	}

	requisites, ok := order["cart"].(string)
	if !ok {
		return models.DetailsRequisites{}, errors.New("не удалось получить 'cart'")
	}

	amountRaw, ok := order["amount"].(string)
	if !ok {
		return models.DetailsRequisites{}, errors.New("не удалось получить 'amount'")
	}

	untilAtRaw, ok := order["time_unix"].(int64)
	if !ok {
		return models.DetailsRequisites{}, errors.New("не удалось получить 'time_unix'")
	}

	amount, err := strconv.ParseFloat(amountRaw, 64)
	if err != nil {
		return models.DetailsRequisites{}, errors.New("не удалось конвертировать 'amount'")
	}

	untilAt := time.Unix(untilAtRaw, 0).Format("2006-01-02 15:04:05")

	return models.DetailsRequisites{
		ID:         externalOrderID,
		AmountIn:   amount,
		UntilAt:    untilAt,
		Requisites: requisites,
		Details:    data,
	}, nil

}
