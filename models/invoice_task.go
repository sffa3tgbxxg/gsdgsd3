package models

import (
	"errors"
	"net/url"
	"time"
)

type InvoiceTask struct {
	Invoice    Invoice     `json:"invoice"`
	Exchangers []Exchanger `json:"exchangers"`
}

type Invoice struct {
	ID        uint64    `json:"id"`
	CreatedAt time.Time `json:"created_at"`
}

type Exchanger struct {
	ID        uint32  `json:"id"`
	Endpoint  string  `json:"endpoint"`
	Name      string  `json:"name"`
	Amount    float64 `json:"amount"`
	APIKey    string  `json:"api_key"`
	SecretKey string  `json:"secret_key"`
	Callback  string  `json:"callback"`
}

type DetailsRequisites struct {
	ID         string                 `json:"id"`
	AmountIn   float64                `json:"amount_in"`
	Requisites string                 `json:"requisites"`
	UntilAt    string                 `json:"until_at"`
	Details    map[string]interface{} `json:"details"`
}

type ExchangerWithInvoices struct {
	ServiceID uint64             `json:"service_id"`
	Exchanger Exchanger          `json:"exchanger"`
	Invoices  []InvoiceCheckLite `json:"invoices"`
}

type InvoiceCheck struct {
	ID         uint64    `json:"id"`
	ExternalID string    `json:"external_id"`
	ServiceID  uint64    `json:"service_id"`
	Exchanger  Exchanger `json:"exchanger"`
}
type InvoiceCheckLite struct {
	ID         uint64 `json:"id"`
	ExternalID string `json:"external_id"`
}

func (t *InvoiceTask) Validate() error {
	if t.Invoice.ID <= 0 {
		return errors.New("invalid invoice ID")
	}

	if len(t.Exchangers) <= 0 {
		return errors.New("no exchangers provided")
	}

	for _, ex := range t.Exchangers {
		if err := ex.Validate(); err != nil {
			return err
		}
	}

	return nil
}

func (e *Exchanger) Validate() error {
	if e.ID <= 0 {
		return errors.New("invalid exchanger ID")
	}
	if e.Name == "" {
		return errors.New("exchanger name is empty")
	}
	if e.Amount <= 0 {
		return errors.New("invalid exchanger amount")
	}
	if e.APIKey == "" {
		return errors.New("exchanger API Key is empty")
	}
	if _, err := url.ParseRequestURI(e.Endpoint); err != nil {
		return errors.New("invalid exchanger endpoint")
	}
	return nil
}
