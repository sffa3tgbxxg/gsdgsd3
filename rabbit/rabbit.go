package rabbit

import (
	"github.com/streadway/amqp"
	"log"
)

type RabbitMQ struct {
	conn    *amqp.Connection
	channel *amqp.Channel
}

// NewRabbitMQ создаёт новое подключение к RabbitMQ
func NewRabbitMQ(url string) (*RabbitMQ, error) {
	conn, err := amqp.Dial(url)
	if err != nil {
		log.Printf("Ошибка подключения к RabbitMQ: %v", err)
		return nil, err
	}

	ch, err := conn.Channel()
	if err != nil {
		log.Printf("Ошибка создания канала: %v", err)
		conn.Close()
		return nil, err
	}

	// Объявляем exchange
	err = ch.ExchangeDeclare(
		"invoices_exchange", // имя exchange
		"direct",            // тип
		true,                // durable
		false,               // auto-deleted
		false,               // internal
		false,               // no-wait
		nil,                 // args
	)
	if err != nil {
		log.Printf("Ошибка объявления exchange: %v", err)
		ch.Close()
		conn.Close()
		return nil, err
	}

	// Объявляем dead-letter exchange
	err = ch.ExchangeDeclare(
		"dead_letter_exchange", // имя dead-letter exchange
		"direct",               // тип
		true,                   // durable
		false,                  // auto-deleted
		false,                  // internal
		false,                  // no-wait
		nil,                    // args
	)
	if err != nil {
		log.Printf("Ошибка объявления dead-letter exchange: %v", err)
		ch.Close()
		conn.Close()
		return nil, err
	}

	// Объявляем очередь
	_, err = ch.QueueDeclare(
		"invoices", // имя очереди
		true,       // durable
		false,      // auto-deleted
		false,      // exclusive
		false,      // no-wait
		amqp.Table{
			"x-dead-letter-exchange":    "dead_letter_exchange",
			"x-dead-letter-routing-key": "dead_letter",
		},
	)
	if err != nil {
		log.Printf("Ошибка объявления очереди: %v", err)
		ch.Close()
		conn.Close()
		return nil, err
	}

	// Объявляем dead-letter очередь
	_, err = ch.QueueDeclare(
		"dead_letter_queue", // имя dead-letter очереди
		true,                // durable
		false,               // auto-deleted
		false,               // exclusive
		false,               // no-wait
		nil,                 // args
	)
	if err != nil {
		log.Printf("Ошибка объявления dead-letter очереди: %v", err)
		ch.Close()
		conn.Close()
		return nil, err
	}

	// Привязываем dead-letter очередь к dead-letter exchange
	err = ch.QueueBind(
		"dead_letter_queue",    // очередь
		"dead_letter",          // routing key
		"dead_letter_exchange", // exchange
		false,                  // no-wait
		nil,                    // args
	)
	if err != nil {
		log.Printf("Ошибка привязки dead-letter очереди: %v", err)
		ch.Close()
		conn.Close()
		return nil, err
	}

	// Привязываем очередь к exchange
	err = ch.QueueBind(
		"invoices",          // очередь
		"invoice.create",    // routing key
		"invoices_exchange", // exchange
		false,               // no-wait
		nil,                 // args
	)
	if err != nil {
		log.Printf("Ошибка привязки очереди: %v", err)
		ch.Close()
		conn.Close()
		return nil, err
	}

	log.Println("RabbitMQ настроен: exchange=invoices_exchange, queue=invoices, routing_key=invoice.create, dead_letter_queue=dead_letter_queue")
	return &RabbitMQ{conn: conn, channel: ch}, nil
}

// NewChannel создаёт новый канал
func (r *RabbitMQ) NewChannel() (*amqp.Channel, error) {
	ch, err := r.conn.Channel()
	if err != nil {
		log.Printf("Ошибка создания нового канала: %v", err)
		return nil, err
	}
	return ch, nil
}

// Consume запускает чтение сообщений из очереди
func (r *RabbitMQ) Consume(queue string) (<-chan amqp.Delivery, error) {
	log.Printf("Начинаем потребление из очереди %s", queue)
	return r.channel.Consume(
		queue, // имя очереди
		"",    // consumer tag
		false, // auto-ack
		false, // exclusive
		false, // no-local
		false, // no-wait
		nil,   // args
	)
}

// Close закрывает соединение
func (r *RabbitMQ) Close() {
	if r.channel != nil {
		if err := r.channel.Close(); err != nil {
			log.Printf("Ошибка закрытия канала: %v", err)
		}
	}
	if r.conn != nil {
		if err := r.conn.Close(); err != nil {
			log.Printf("Ошибка закрытия соединения: %v", err)
		}
	}
}
