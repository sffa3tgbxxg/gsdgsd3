package clickhouse

import (
	"database/sql"
	"fmt"
	_ "github.com/ClickHouse/clickhouse-go"
	"log"
	"os"
	"payment-service-go/models"
	"time"
)

type ClickDB struct {
	db *sql.DB
}

func NewClickDB() (*ClickDB, error) {
	chHost := os.Getenv("CLICKHOUSE_HOST")     // localhost
	chPort := os.Getenv("CLICKHOUSE_PORT")     // 9000
	chDB := os.Getenv("CLICKHOUSE_DATABASE")   // paymentswh
	chUser := os.Getenv("CLICKHOUSE_USERNAME") // default
	chPass := os.Getenv("CLICKHOUSE_PASSWORD") // 12345678

	dsn := fmt.Sprintf("tcp://%s:%s?database=%s&username=%s&password=%s",
		chHost, chPort, chDB, chUser, chPass)

	db, err := sql.Open("clickhouse", dsn)
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, err
	}
	log.Println("ClickHouse подключен")
	return &ClickDB{db: db}, nil
}

func (l *ClickDB) LogAnalytics(invoiceID uint64, status, exchangerName string, duration float64, createdAt time.Time) error {
	tx, err := l.db.Begin()
	if err != nil {
		log.Printf("Ошибка начала транзакции (analytics): %v", err)
		return err
	}

	_, err = tx.Exec(`
        INSERT INTO exchangers_analytics (invoice_id, status, exchanger_name, request_duration_ms, created_at, processed_at)
        VALUES (?, ?, ?, ?, ?, ?)
    `, invoiceID, status, exchangerName, duration, createdAt, time.Now())

	if err != nil {
		tx.Rollback()
		log.Printf("Ошибка ClickHouse (analytics): %v", err)
		return err
	}

	if err := tx.Commit(); err != nil {
		log.Printf("Ошибка коммита (analytics): %v", err)
		return err
	}

	log.Printf("ClickHouse: Записано в exchangers_analytics, invoice_id=%d, status=%s", invoiceID, status)
	return nil
}

func (l *ClickDB) LogErrorApiRequests(invoiceID uint64, exchangerId uint32, errorMessage string) error {
	tx, err := l.db.Begin()
	if err != nil {
		log.Printf("Ошибка начала транзакции (analytics): %v", err)
		return err
	}

	_, err = tx.Exec(`
        INSERT INTO api_error_requests (invoice_id, exchanger_id, error_message, time)
        VALUES (?, ?, ?, ?)
    `, invoiceID, exchangerId, errorMessage, time.Now())

	if err != nil {
		errRollback := tx.Rollback()
		if errRollback != nil {
			log.Printf("Не удалось выполнить rollback clickhouse (api_requests): %v", errRollback)
		}

		log.Printf("Ошибка ClickHouse (api_requests): %v", err)
		return err
	}

	if err := tx.Commit(); err != nil {
		log.Printf("Ошибка коммита (api_requests): %v", err)
		return err
	}

	log.Printf("ClickHouse: Записана ошибка для invoice_id=%d, exchanger=%s", invoiceID, exchangerId)
	return nil
}

func (l *ClickDB) LogErrorInvoice(invoice models.Invoice, errorMessage string) error {
	tx, err := l.db.Begin()
	if err != nil {
		log.Printf("Ошибка начала транзакции (analytics): %v", err)
		return err
	}

	timeNow := time.Now().UTC().Format("2006-01-02 15:04:05")

	_, err = tx.Exec(`
        INSERT INTO invoices_errors_logs (invoice_id, error_message, time)
        VALUES (?, ?, ?, ?)
    `, invoice.ID, errorMessage, timeNow)

	if err != nil {
		errRollback := tx.Rollback()
		if errRollback != nil {
			log.Printf("Не удалось выполнить rollback clickhouse (api_requests): %v", errRollback)
		}

		log.Printf("Ошибка ClickHouse (api_requests): %v", err)
		return err
	}

	if err := tx.Commit(); err != nil {
		log.Printf("Ошибка коммита (api_requests): %v", err)
		return err
	}

	log.Printf("ClickHouse: Записана ошибка для invoice_id=%d", invoice.ID)
	return nil
}

func (l *ClickDB) ApiRequests(endpoint string, statusCode int, response string, params string, invoiceId uint64, exchangerId uint32) error {
	tx, err := l.db.Begin()
	if err != nil {
		log.Printf("Ошибка начала транзакции (analytics): %v", err)
		return err
	}

	timeNow := time.Now().UTC().Format("2006-01-02 15:04:05")

	_, err = tx.Exec(`
        INSERT INTO api_requests (invoice_id, exchanger_id, status_code, endpoint, params, response, time)
        VALUES (?, ?, ?, ?)
    `, invoiceId, exchangerId, statusCode, endpoint, params, response, timeNow)

	if err != nil {
		errRollback := tx.Rollback()
		if errRollback != nil {
			log.Printf("Не удалось выполнить rollback clickhouse (api_requests): %v", errRollback)
		}

		log.Printf("Ошибка ClickHouse (api_requests): %v", err)
		return err
	}

	if err := tx.Commit(); err != nil {
		log.Printf("Ошибка коммита (api_requests): %v", err)
		return err
	}

	log.Printf("ClickHouse: записан запрос к апи")
	return nil
}

func (l *ClickDB) InvoiceHistoryInsert(invoiceId uint64, updatedBy string, status string, userId *uint64, details *string) error {
	tx, err := l.db.Begin()
	if err != nil {
		log.Printf("Ошибка начала транзакции (analytics): %v", err)
		return err
	}

	timeNow := time.Now().UTC().Format("2006-01-02 15:04:05")

	_, err = tx.Exec(`
        INSERT INTO invoice_history (invoice_id, status, updated_by, user_id, details, time)
        VALUES (?, ?, ?, ?)
    `, invoiceId, status, updatedBy, userId, details, timeNow)

	if err != nil {
		errRollback := tx.Rollback()
		if errRollback != nil {
			log.Printf("Не удалось выполнить rollback clickhouse (invoice_history): %v", errRollback)
		}

		log.Printf("Ошибка ClickHouse (invoice_history): %v", err)
		return err
	}

	if err := tx.Commit(); err != nil {
		log.Printf("Ошибка коммита (invoice_history): %v", err)
		return err
	}

	log.Printf("ClickHouse: записан запрос к апи")
	return nil
}

func (l *ClickDB) Close() {
	if l.db != nil {
		l.db.Close()
	}
}
