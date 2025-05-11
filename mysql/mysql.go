package mysql

import (
	"database/sql"
	"encoding/json"
	"errors"
	_ "github.com/go-sql-driver/mysql"
	"github.com/joho/godotenv"
	"log"
	"os"
	"payment-service-go/models"
	"strconv"
	"strings"
	"time"
)

type MySQLDB struct {
	db *sql.DB
}

func NewMySQLDB() (*MySQLDB, error) {
	err := godotenv.Load()
	if err != nil {
		log.Fatalf("Ошибка загрузки .env файла: %v", err)
	}

	dbHost := os.Getenv("DB_HOST")
	dbPort := os.Getenv("DB_PORT")
	dbUser := os.Getenv("DB_USERNAME")
	dbPass := os.Getenv("DB_PASSWORD")
	dbName := os.Getenv("DB_DATABASE")

	dsn := dbUser + ":" + dbPass + "@tcp(" + dbHost + ":" + dbPort + ")/" + dbName + "?charset=utf8mb4&parseTime=true"
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, err
	}
	log.Println("MySQL Подключен")
	return &MySQLDB{db: db}, nil
}

func (l *MySQLDB) UpdateInvoice(invoiceID uint64, exchangerId uint32, details models.DetailsRequisites) error {
	detailsJSON, err := json.Marshal(details.Details)
	if err != nil {
		return err
	}

	_, err = l.db.Exec(
		"UPDATE invoices SET external_id = ?, requisites = ?, amount_in = ?, expiry_at = ?, status = ?, exchanger_id = ?, details = ?, updated_at = ? WHERE id = ?",
		details.ID, details.Requisites, details.AmountIn, details.UntilAt, "pending", exchangerId, string(detailsJSON), time.Now().Format("2006-01-02 15:04:05"), invoiceID,
	)
	if err != nil {
		log.Printf("Ошибка MySQL для invoice %d: %v", invoiceID, err)
		return err
	}
	log.Printf("MySQL: Invoice %d обновлён, status=%s, exchanger=%d", invoiceID, "pending", exchangerId)
	return nil
}

func (l *MySQLDB) UpdateGrooupInvoicesStatus(invoicesIDs []uint64, status string) error {
	strArr := make([]string, len(invoicesIDs))
	for i, num := range invoicesIDs {
		strArr[i] = strconv.FormatUint(num, 10)
	}

	invoicesIDsForQuery := strings.Join(strArr, ",")

	_, err := l.db.Exec(
		"UPDATE invoices SET status = ? WHERE id IN (?)", status, invoicesIDsForQuery)

	if err != nil {
		log.Printf("Ошибка MySQL при обработке массово инвойсов %v", err)
		return err
	}

	return nil
}

func (l *MySQLDB) UpdateInvoiceStatus(invoice models.InvoiceCheckLite, status string) error {
	_, err := l.db.Exec(
		"UPDATE invoices SET status = ?, updated_at  = ? WHERE id = ? AND external_id = ?",
		status, time.Now().Format("2006-01-02 15:04:05"), invoice.ID, invoice.ExternalID,
	)

	if err != nil {
		log.Printf("Ошибка MySQL для invoice %d: %v", invoice.ID, err)
		return err
	}

	log.Printf("MySQL: Invoice %d обновлён, status=%s", invoice.ID, status)
	return nil
}

func (l *MySQLDB) GetInvoiceByExternalIDAndServiceID(externalID string, serviceID uint64) (*models.InvoiceCheckLite, error) {
	var invoice models.InvoiceCheckLite

	row := l.db.QueryRow("SELECT id, external_id FROM invoices WHERE external_id = ? AND service_id = ? LIMIT 1", externalID, serviceID)

	err := row.Scan(&invoice.ID, &invoice.ExternalID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}

	return &invoice, nil

}

func (l *MySQLDB) CustomQuery(query string, args ...interface{}) error {
	_, err := l.db.Exec(query, args...)

	return err
}

func (l *MySQLDB) GetInvoicesByStatus(status string, date string) ([]models.InvoiceCheck, error) {
	rows, err := l.db.Query(
		"SELECT i.id, i.external_id, i.amount_in, i.service_id, e.name, e.endpoint, se.api_key FROM invoices i INNER JOIN service_exchangers se ON se.service_id = i.service_id INNER JOIN exchangers e ON e.id = i.exchanger_id AND se.exchanger_id = e.id WHERE i.status = ? AND i.expiry_at <= ? AND i.external_id IS NOT NULL AND i.expiry_at IS NOT NULL ORDER BY e.id",
		status, date,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var invoices []models.InvoiceCheck

	for rows.Next() {
		var invoice models.InvoiceCheck
		err := rows.Scan(
			&invoice.ID,
			&invoice.ExternalID,
			&invoice.Exchanger.Amount,
			&invoice.ServiceID,
			&invoice.Exchanger.Name,
			&invoice.Exchanger.Endpoint,
			&invoice.Exchanger.APIKey,
		)
		if err != nil {
			return nil, err
		}
		invoices = append(invoices, invoice)
	}

	if err = rows.Err(); err != nil {
		return nil, err
	}
	return invoices, nil
}

func (l *MySQLDB) Close() {
	if l.db != nil {
		l.db.Close()
	}
}
