package exchanger

import "payment-service-go/models"

type Exchanger interface {
	GetRequisites(task models.InvoiceTask, ex models.Exchanger) (models.DetailsRequisites, error)
	ReturnFormattedDetails(data map[string]interface{}) (models.DetailsRequisites, error)
	CheckInvoices(invoices []models.InvoiceCheckLite, serviceID uint64) error
}
