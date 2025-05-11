package main

import (
	"encoding/json"
	"github.com/joho/godotenv"
	"github.com/streadway/amqp"
	"log"
	"os"
	"payment-service-go/exchanger"
	"payment-service-go/models"
	"payment-service-go/rabbit"
	"sync"
	"sync/atomic"
	"time"
)

type App struct {
	isProcessing      int32
	isProcessingCheck int32
	rabbitConn        *rabbit.RabbitMQ
	channel           *amqp.Channel
	consumer          <-chan amqp.Delivery
}

func NewApp(rabbitConn *rabbit.RabbitMQ) (*App, error) {
	ch, err := rabbitConn.NewChannel()
	if err != nil {
		return nil, err
	}
	consumer, err := ch.Consume("invoices", "", false, false, false, false, nil)
	if err != nil {
		log.Printf("Ошибка потребителя: %v", err)
		ch.Close()
		return nil, err
	}
	return &App{isProcessing: 0, isProcessingCheck: 0, rabbitConn: rabbitConn, channel: ch, consumer: consumer}, nil
}

func main() {
	err := godotenv.Load()
	if err != nil {
		log.Fatalf("Ошибка загрузки .env файла: %v", err)
	}

	rbHost := os.Getenv("RABBITMQ_HOST")
	rbPort := os.Getenv("RABBITMQ_PORT")
	rbUser := os.Getenv("RABBITMQ_USER")
	rbPass := os.Getenv("RABBITMQ_PASSWORD")

	rabbitConn, err := rabbit.NewRabbitMQ("amqp://" + rbUser + ":" + rbPass + "@" + rbHost + ":" + rbPort + "/")
	if err != nil {
		log.Fatalf("RabbitMQ error: %v", err)
	}
	defer rabbitConn.Close()

	app, err := NewApp(rabbitConn)
	if err != nil {
		log.Fatalf("App error: %v", err)
	}
	defer app.channel.Close()

	processor := exchanger.NewProcessor()
	app.startProcessing(processor)
}

func (a *App) startProcessing(processor *exchanger.Processor) {
	// Ticker для обработки очереди
	tickerQueue := time.NewTicker(20 * time.Second)
	defer tickerQueue.Stop()
	go func() {
		log.Println("Запуск процессинга очереди RabbitMQ…")
		for range tickerQueue.C {
			a.processQueue(processor)
		}
	}()

	// Ticker для ProcessInvoices
	tickerCheck := time.NewTicker(2 * time.Minute)
	defer tickerCheck.Stop()
	go func() {
		log.Println("Запуск проверки счетов…")
		for range tickerCheck.C {
			if err := processor.ProcessInvoices(); err != nil {
				log.Printf("Ошибка при ProcessInvoices: %v", err)
			}
		}
	}()

	// Блокируем main-горутину (или ждём сигнала на shutdown)
	select {}
}

func (a *App) processQueue(processor *exchanger.Processor) {
	if !a.startQueueProcessing() {
		log.Println("Обработка идёт, пропуск")
		return
	}
	defer a.stopQueueProcessing()

	msgs, err := a.getMessages()
	if err != nil {
		log.Printf("Ошибка сообщений: %v", err)
		return
	}
	if len(msgs) == 0 {
		log.Println("Очередь пуста")
		return
	}
	log.Printf("Получено %d сообщений", len(msgs))

	const maxConcurrent = 50
	sem := make(chan struct{}, maxConcurrent)
	var wg sync.WaitGroup

	for _, msg := range msgs {
		sem <- struct{}{}
		wg.Add(1)
		go func(msg amqp.Delivery) {
			defer wg.Done()
			defer func() { <-sem }()
			a.handleMessage(msg, processor)
		}(msg)
	}
	wg.Wait()
}

func (a *App) getMessages() ([]amqp.Delivery, error) {
	var messages []amqp.Delivery
	timeout := time.After(2 * time.Second)
	for len(messages) < 100 {
		select {
		case msg, ok := <-a.consumer:
			if !ok {
				log.Println("Канал закрыт")
				return messages, nil
			}
			messages = append(messages, msg)
			log.Printf("Сообщение: %s", msg.Body)
		case <-timeout:
			log.Printf("Сбор завершён, сообщений: %d", len(messages))
			return messages, nil
		}
	}
	log.Printf("Лимит достигнут, сообщений: %d", len(messages))
	return messages, nil
}

func (a *App) startQueueProcessing() bool {
	return atomic.CompareAndSwapInt32(&a.isProcessing, 0, 1)
}

func (a *App) stopQueueProcessing() {
	atomic.StoreInt32(&a.isProcessing, 0)
}

func (a *App) handleMessage(msg amqp.Delivery, processor *exchanger.Processor) {
	task, err := a.parseTask(msg.Body)
	if err != nil {
		msg.Nack(false, false)
		log.Printf("JSON ошибка: %v", err)
		return
	}
	if err := task.Validate(); err != nil {
		msg.Nack(false, false)
		processor.MysqlLogger.CustomQuery(
			"UPDATE invoices SET status = ?, updated_at = ? WHERE id = ?",
			"cancel_invalid",
			time.Now().Format("2006-01-02 15:04:05"),
			task.Invoice.ID,
		)

		processor.ClickLogger.LogErrorInvoice(task.Invoice, "Невалидная задача: "+err.Error())
		log.Printf("Невалидная задача %d: %v", task.Invoice.ID, err)
		return
	}
	if a.isTaskExpired(task.Invoice.CreatedAt) {
		msg.Nack(false, false)
		processor.MysqlLogger.CustomQuery(
			"UPDATE invoices SET status = ?, updated_at = ? WHERE id = ?",
			"cancel_search",
			time.Now().Format("2006-01-02 15:04:05"),
			task.Invoice.ID,
		)
		log.Printf("Заявка %d просрочена", task.Invoice.ID)
		return
	}
	if a.processTask(processor, task) {
		msg.Ack(false)
		log.Printf("Заявка %d обработана", task.Invoice.ID)
	} else {
		msg.Nack(false, true)
		log.Printf("Заявка %d: реквизиты не найдены", task.Invoice.ID)
	}
}

func (a *App) parseTask(body []byte) (models.InvoiceTask, error) {
	var task models.InvoiceTask
	return task, json.Unmarshal(body, &task)
}

func (a *App) isTaskExpired(createdAt time.Time) bool {
	return time.Since(createdAt) > 5*time.Minute
}

func (a *App) processTask(processor *exchanger.Processor, task models.InvoiceTask) bool {
	requisites, err := processor.Process(task)
	if err != nil {
		log.Printf("Ошибка заявки %d: %v", task.Invoice.ID, err)
		return false
	}
	log.Printf("Реквизиты %d: %s", task.Invoice.ID, requisites)
	return true
}
