package exchanger

import (
	"errors"
	"fmt"
	"math/rand"
	"payment-service-go/models"
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

	result := map[string]interface{}{
		"id":         fmt.Sprintf("%d", rand.Int63()),
		"requisites": fmt.Sprintf("%d-7777-1111-%d", task.Invoice.ID, rand.Int63()),
		"details": map[string]interface{}{
			"message": "Test Exchanger is working",
		},
	}
	return t.ReturnFormattedDetails(result)
}

func (t *TestExchanger) ReturnFormattedDetails(data map[string]interface{}) (models.DetailsRequisites, error) {
	id, ok := data["id"].(string)
	if !ok {
		return models.DetailsRequisites{}, errors.New("Не удалось получить 'ID'")
	}
	requisites, ok := data["requisites"].(string)
	if !ok {
		return models.DetailsRequisites{}, errors.New("Не удалось получить 'Requisites'")
	}

	details, ok := data["requisites"].(map[string]interface{})
	if !ok {
		return models.DetailsRequisites{}, errors.New("Не удалось получить 'Details'")
	}

	detailsData := map[string]interface{}{
		"data":    data,
		"details": details,
	}

	return models.DetailsRequisites{
		ID:         id,
		Requisites: requisites,
		Details:    detailsData,
	}, nil
}
