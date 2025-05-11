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
	"payment-service-go/models"
	"time"
)

type BitlogaExchanger struct {
	config    models.Exchanger
	processor Processor
}

func NewBitlogaExchanger(config models.Exchanger, processor *Processor) *BitlogaExchanger {
	return &BitlogaExchanger{config: config, processor: *processor}
}

func (g *BitlogaExchanger) CheckInvoices(invoices []models.InvoiceCheckLite, serviceID uint64) error {
	client := &http.Client{Timeout: 8 * time.Second}

	// Шаблон тела
	bodyMap := map[string]interface{}{
		"action": "details",
	}

	tryRequest := func() (*http.Response, []byte, error) {
		reqBody, err := json.Marshal(bodyMap)
		if err != nil {
			return nil, nil, err
		}

		req, err := http.NewRequest("POST", g.config.Endpoint+"/api/v1/order/", bytes.NewBuffer(reqBody))
		if err != nil {
			return nil, nil, err
		}

		h := hmac.New(sha512.New, []byte(g.config.SecretKey))
		h.Write([]byte(reqBody))
		signature := fmt.Sprintf("%x", h.Sum(nil))

		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
		req.Header.Set("X-APIKEY", g.config.APIKey)
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

		err = g.processStatusInvoice(processor, invoice, status)

		if err != nil {
			fmt.Errorf("[Bitloga] не удалось обрабатотать статус у InvoiceID: %v, error: %v", invoice.ID, err)
			continue
		}
	}

	return nil
}

func (g *BitlogaExchanger) processStatusInvoice(processor *Processor, invoice models.InvoiceCheckLite, orderStatus string) error {
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

func (g *BitlogaExchanger) GetRequisites(task models.InvoiceTask, ex models.Exchanger) (models.DetailsRequisites, error) {

	// Данные для запроса
	data := map[string]interface{}{
		"action":   "invoice",
		"uniqueid": fmt.Sprintf("%v", task.Invoice.ID),
		"paysys":   "RUBBALANCE",
		"amount":   ex.Amount,
		"comis":    "payer",
		"name":     "test",  // Замените на "Тест", если сервер принимает кириллицу
		"surname":  "test2", // Замените на "Тест", если сервер принимает кириллицу
	}

	reqBody, err := json.Marshal(data)
	if err != nil {
		fmt.Println("Ошибка сериализации JSON:", err)
		return models.DetailsRequisites{}, err
	}
	fmt.Println("JSON тело:", string(reqBody))

	req, err := http.NewRequest("POST", ex.Endpoint+"/api/v1/", bytes.NewBuffer(reqBody))
	if err != nil {
		return models.DetailsRequisites{}, err
	}

	h := hmac.New(sha512.New, []byte(ex.SecretKey))
	h.Write(reqBody)
	signature := fmt.Sprintf("%x", h.Sum(nil))

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-APIKEY", ex.APIKey)
	req.Header.Set("X-SIGNATURE", signature)

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
	log.Printf("result: %v", result)

	if result["success"] != true {
		var msg string

		if m, ok := result["message"].(string); ok {
			msg = m
		} else if r, ok := result["response"].(string); ok {
			msg = r
		} else {
			msg = "неизвестная ошибка от сервера"
		}

		return models.DetailsRequisites{}, errors.New(msg)
	}

	return g.ReturnFormattedDetails(result)
}

func (g *BitlogaExchanger) ReturnFormattedDetails(data map[string]interface{}) (models.DetailsRequisites, error) {
	id, ok := data["invoiceid"].(string)
	if !ok {
		return models.DetailsRequisites{}, errors.New("'id' is empty")
	}

	requisites, ok := data["requisites"].(string)
	if !ok {
		return models.DetailsRequisites{}, errors.New("'requisites' не строка")
	}

	amountIn, ok := data["amount_payable"].(float64)
	if !ok {
		return models.DetailsRequisites{}, errors.New("'amount_payable' не число")
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
