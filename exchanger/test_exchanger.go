package exchanger

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/ioutil"
	"log"
	"net/http"
	"payment-service-go/models"
	"time"
)

type TestExchanger struct {
	config models.Exchanger
}

func NewTestExchanger(config models.Exchanger) *TestExchanger {
	return &TestExchanger{config: config}
}

func (t *TestExchanger) CheckInvoices(invoices []models.InvoiceCheckLite, serviceID uint64) error {
	//reqBody, err := json.Marshal(map[string]interface{}{
	//	"order_id": invoices,
	//})
	return nil
}

func (t *TestExchanger) GetRequisites(task models.InvoiceTask, ex models.Exchanger) (models.DetailsRequisites, error) {
	reqBody, err := json.Marshal(map[string]interface{}{
		"invoice_id": task.Invoice.ID,
		"amount":     ex.Amount,
	})
	if err != nil {
		return models.DetailsRequisites{}, err
	}

	log.Printf("Запрос к %s: %s", ex.Endpoint+"/api/requisites", string(reqBody))
	req, err := http.NewRequest("POST", ex.Endpoint+"/api/requisites", bytes.NewBuffer(reqBody))
	if err != nil {
		return models.DetailsRequisites{}, err
	}
	req.Header.Set("Authorization", "Bearer "+ex.APIKey)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return models.DetailsRequisites{}, err
	}
	defer resp.Body.Close()

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return models.DetailsRequisites{}, err
	}
	log.Printf("Ответ: status=%d, body=%s", resp.StatusCode, string(body))

	if resp.StatusCode != 200 {
		return models.DetailsRequisites{}, errors.New("сервер вернул ошибку: " + string(body))
	}

	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		return models.DetailsRequisites{}, err
	}

	if result["status"] == "success" {
		return t.ReturnFormattedDetails(result)

	}
	return models.DetailsRequisites{}, errors.New("неверный формат ответа")
}

func (t *TestExchanger) ReturnFormattedDetails(data map[string]interface{}) (models.DetailsRequisites, error) {
	rawData, ok := data["data"]
	if !ok {
		return models.DetailsRequisites{}, errors.New("ключ 'data' отсутствует")
	}

	inner, ok := rawData.(map[string]interface{})
	if !ok {
		// тут лог покажет что именно пришло
		log.Printf("не удалось привести 'data': %#v", rawData)
		return models.DetailsRequisites{}, errors.New("'data' не является объектом (map[string]interface{})")
	}

	// приводим id
	id, ok := inner["id"].(string)
	if !ok {
		return models.DetailsRequisites{}, errors.New("'id' не число")
	}

	requisites, ok := inner["requisites"].(string)
	if !ok {
		return models.DetailsRequisites{}, errors.New("'requisites' не строка")
	}

	return models.DetailsRequisites{
		ID:         id,
		Requisites: requisites,
		Details:    inner,
	}, nil
}
