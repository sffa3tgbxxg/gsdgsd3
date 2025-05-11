package exchanger

import (
	"errors"
	"fmt"
	"log"
	"payment-service-go/clickhouse"
	"payment-service-go/models"
	"payment-service-go/mysql"
	"time"
)

type Processor struct {
	MysqlLogger *mysql.MySQLDB
	ClickLogger *clickhouse.ClickDB
}

// NewProcessor - конструктор
func NewProcessor() *Processor {
	mysqlLogger, err := mysql.NewMySQLDB()
	if err != nil {
		log.Fatalf("Ошибка MySQL logger: %v", err)
	}
	clickLogger, err := clickhouse.NewClickDB()
	if err != nil {
		log.Fatalf("Ошибка Clickhouse logger: %v", err)
	}

	return &Processor{
		MysqlLogger: mysqlLogger,
		ClickLogger: clickLogger,
	}
}

// Process - обрабатывает задачу
func (p *Processor) Process(task models.InvoiceTask) (string, error) {
	// Перебираем обменники из задачи
	for _, ex := range task.Exchangers {
		// Создаём обменник на основе имени
		var exchanger Exchanger
		switch ex.Name {
		case "Bitloga":
			exchanger = NewBitlogaExchanger(ex, p) // Передаём всю структуру Exchanger
			break
		case "Greengo":
			exchanger = NewGreengoExchanger(ex, p)
			break
		case "LuckyPay":
			exchanger = NewLuckyPayExchanger(ex)
			break
		case "Test":
			exchanger = NewTestExchanger(ex)
			break
		default:
			log.Printf("Обменник %s не поддерживается", ex.Name)
			continue
		}

		// Запрашиваем реквизиты
		requisites, err := exchanger.GetRequisites(task, ex)
		if err == nil {
			log.Printf("Реквизиты найдены через %s: %s", ex.Name, requisites.Requisites)
			p.SuccessGetRequisites(task, ex, requisites)
			return requisites.Requisites, nil
		} else {
			// Логируем ошибку в ClickHouse (api_requests)
			p.ClickLogger.LogErrorApiRequests(task.Invoice.ID, ex.ID, "Не удалось получить реквизиты: "+err.Error())
			log.Printf("Ошибка в %s: %v", ex.Name, err)
			continue
		}

	}
	return "", errors.New("реквизиты не найдены ни одним обменником")
}

func (p *Processor) ProcessInvoices() error {
	now := time.Now().Format("2006-01-02 15:04:05")
	invoices, err := p.MysqlLogger.GetInvoicesByStatus("pending", now)

	if err != nil {
		return err
	}
	grouped := make(map[string]*models.ExchangerWithInvoices)

	for _, inv := range invoices {
		key := fmt.Sprintf("%d:%d", inv.ServiceID, inv.Exchanger.ID)

		if _, exists := grouped[key]; !exists {
			grouped[key] = &models.ExchangerWithInvoices{
				ServiceID: inv.ServiceID,
				Exchanger: inv.Exchanger,
				Invoices:  []models.InvoiceCheckLite{},
			}
		}

		grouped[key].Invoices = append(grouped[key].Invoices, models.InvoiceCheckLite{
			ID:         inv.ID,
			ExternalID: inv.ExternalID,
		})
	}
	for _, group := range grouped {
		var exchanger Exchanger
		switch group.Exchanger.Name {
		case "Greengo":
			exchanger = NewGreengoExchanger(group.Exchanger, p)
			break
		case "LuckyPay":
			exchanger = NewLuckyPayExchanger(group.Exchanger)
			break
		default:
			p.cancelInvoices(group.Invoices)
			continue
		}

		err := exchanger.CheckInvoices(group.Invoices, group.ServiceID)

		if err != nil {
			return fmt.Errorf("Не удалось проверить счета error: %v", err)
		}
	}

	return nil
}

func (p *Processor) cancelInvoices(invoices []models.InvoiceCheckLite) {
	var IDs []uint64

	for _, inv := range invoices {
		IDs = append(IDs, inv.ID)
	}

	err := p.MysqlLogger.UpdateGrooupInvoicesStatus(IDs, "cancel_time")
	if err != nil {
		log.Printf("Не удалось отменить массово счета. Error: %v", err)
	}
}

func (p *Processor) SuccessGetRequisites(task models.InvoiceTask, exchangerTask models.Exchanger, details models.DetailsRequisites) error {
	err := p.MysqlLogger.UpdateInvoice(task.Invoice.ID, exchangerTask.ID, details)
	if err != nil {
		return err
	}
	return nil
}
