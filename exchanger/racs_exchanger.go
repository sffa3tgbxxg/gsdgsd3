package exchanger

import (
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

	tryRequest := func(invoiceID string) (*http.Response, []byte, error) {
		data := url.Values{}
		data.Set("id", invoiceID)
		encoded := data.Encode()

		urlApi := r.config.Endpoint + "/flat_api/status?" + encoded
		req, err := http.NewRequest("POST", urlApi, nil)
		if err != nil {
			return nil, nil, err
		}

		req.Header.Set("Accept", "application/json")
		req.Header.Set("Authorization", "Bearer "+r.config.APIKey)

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

	for _, invoice := range invoices {
		_, body, err := tryRequest(invoice.ExternalID)
		if err != nil {
			r.processor.ClickLogger.LogErrorApiRequests(invoice.ID, r.config.ID, string(body))
			fmt.Errorf("[Racks] не удалось проверить счет InvoiceID: %v, error: %v", invoice.ID, err)
			continue
		}
		var result map[string]interface{}
		if err := json.Unmarshal(body, &result); err != nil {
			r.processor.ClickLogger.LogErrorApiRequests(invoice.ID, r.config.ID, string(body))
			fmt.Errorf("[Racks] не удалось проверить счет InvoiceID: %v, error: %v", invoice.ID, err)
			continue
		}

		status, ok := result["status"].(string)
		if !ok {
			r.processor.ClickLogger.LogErrorApiRequests(invoice.ID, r.config.ID, string(body))
			fmt.Errorf("[Racks] не удалось получить статус у InvoiceID: %v, error: %v", invoice.ID, err)
			continue
		}

		err = r.processStatusInvoice(&r.processor, invoice, status)

		if err != nil {
			fmt.Errorf("[Racks] не удалось обрабатотать статус у InvoiceID: %v, error: %v", invoice.ID, err)
			continue
		}
	}

	return nil
}

func (r *RacksExchanger) processStatusInvoice(processor *Processor, invoice models.InvoiceCheckLite, orderStatus string) error {
	var status string
	switch orderStatus {
	case "Done":
		status = "paid"
	case "Pending":
		return nil
	case "Cancel":
		status = "cancel_time"
	default:
		return errors.New("Не получилось обработать статус")
	}

	err := processor.MysqlLogger.UpdateInvoiceStatus(invoice, status)
	details := "OrderStatus: " + orderStatus
	r.processor.ClickLogger.InvoiceHistoryInsert(invoice.ID, "golang_process_status", status, nil, &details)
	if err != nil {
		return err
	}

	return nil
}

func (r *RacksExchanger) GetRequisites(task models.InvoiceTask, ex models.Exchanger) (models.DetailsRequisites, error) {

	data := url.Values{}
	data.Set("amount", strconv.FormatFloat(ex.Amount, 'f', -1, 64))
	data.Set("typecommission", "1")
	data.Set("private_key", ex.SecretKey)
	data.Set("currency", "RUB")
	data.Set("callback", ex.Callback)

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
