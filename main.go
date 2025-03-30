package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	_ "github.com/lib/pq"
	"github.com/oklog/ulid/v2"
	"github.com/pressly/goose/v3"
	amqp "github.com/rabbitmq/amqp091-go"
	"golang.org/x/crypto/bcrypt"
	"golang.org/x/sync/errgroup"
)

type App struct {
	db          *sql.DB
	amqpChannel *amqp.Channel
	logger      *slog.Logger
}

type OutboxEvent struct {
	ID          int
	Name        string
	Payload     []byte
	PublishedAt time.Time
}

func main() {
	err := run()
	if err != nil {
		log.Println(err)
		os.Exit(1)
	}
}

func run() error {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))
	g, ctx := errgroup.WithContext(context.Background())

	db, err := sql.Open("postgres", os.Getenv("DATABASE_URL"))
	if err != nil {
		return fmt.Errorf("unable to open conn to postgres: %v", err)
	}

	err = db.PingContext(ctx)
	if err != nil {
		return fmt.Errorf("unable to ping postgres: %v", err)
	}

	err = goose.Up(db, "./sql")
	if err != nil {
		return fmt.Errorf("unable to apply migrations: %v", err)
	}

	amqpConn, err := amqp.Dial(os.Getenv("AMQP_URL"))
	if err != nil {
		return fmt.Errorf("unable to dial RabbitMQ: %v", err)
	}

	amqpChannel, err := amqpConn.Channel()
	if err != nil {
		return err
	}

	app := &App{
		db:          db,
		logger:      logger,
		amqpChannel: amqpChannel,
	}

	err = app.setupRabbitMq()
	if err != nil {
		return err
	}

	g.Go(func() error {
		logger.Info("running the http service")
		return app.startHttpService()
	})

	g.Go(func() error {
		logger.Info("running amqp event handlers")
		return app.startEventHandlers(ctx)
	})

	g.Go(func() error {
		logger.Info("running outbox publisher")
		err = app.startOutboxPublisherScheduler(ctx)
		if err != nil {
			logger.Error("error on outbox publisher scheduler", "error", err)
			return err
		}

		return nil
	})

	return g.Wait()
}

type CreateAccountRequest struct {
	Email     string `json:"email"`
	Password  string `json:"password"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
}

type AccountCreatedEvent struct {
	AccountID string    `json:"account_id"`
	Email     string    `json:"email"`
	CreatedAt time.Time `json:"created_at"`
}

func (a *App) startHttpService() error {
	router := echo.New()
	router.HideBanner = true

	router.Use(middleware.Recover())
	router.Use(middleware.Logger())
	router.POST("/accounts", func(c echo.Context) error {
		var body CreateAccountRequest
		err := c.Bind(&body)
		if err != nil {
			return err
		}

		rctx := c.Request().Context()
		accountID := ulid.Make().String()
		hashedPassword, err := bcrypt.GenerateFromPassword([]byte(body.Password), bcrypt.DefaultCost)
		if err != nil {
			return err
		}

		tx, err := a.db.Begin()
		if err != nil {
			return err
		}

		_, err = tx.ExecContext(
			rctx,
			`INSERT INTO accounts (id, first_name, last_name, email, password) VALUES ($1, $2, $3, $4, $5);`,
			accountID,
			body.FirstName,
			body.LastName,
			body.Email,
			hashedPassword,
		)
		if err != nil {
			a.logger.Debug(err.Error())
			_ = tx.Rollback()
			return err
		}

		err = a.publishEventToOutbox(rctx, tx, "AccountCreated", &AccountCreatedEvent{
			AccountID: accountID,
			Email:     body.Email,
			CreatedAt: time.Now().UTC(),
		})
		if err != nil {
			_ = tx.Rollback()
			return err
		}

		err = tx.Commit()
		if err != nil {
			return fmt.Errorf("error committing transaction: %v", err)
		}

		return c.JSON(http.StatusCreated, map[string]string{"message": "account created successfully"})
	})

	return router.Start(fmt.Sprintf(":%s", os.Getenv("HTTP_PORT")))
}

func (a *App) publishEvent(ctx context.Context, name string, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	err = a.amqpChannel.PublishWithContext(ctx, name, "", false, false, amqp.Publishing{
		ContentType: "application/json",
		Body:        body,
	})
	if err != nil {
		return err
	}

	return nil
}

func (a *App) publishEventToOutbox(ctx context.Context, tx *sql.Tx, name string, payload any) error {
	marshalledPayload, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	_, err = tx.ExecContext(
		ctx,
		`INSERT INTO outbox_events (name, payload) VALUES ($1, $2)`,
		name,
		marshalledPayload,
	)
	if err != nil {
		a.logger.Debug(err.Error())
		return err
	}

	return nil
}

func (a *App) startEventHandlers(ctx context.Context) error {
	q, err := a.amqpChannel.QueueDeclare("", false, false, true, false, nil)
	if err != nil {
		return err
	}

	err = a.amqpChannel.QueueBind(q.Name, "", "AccountCreated", false, nil)
	if err != nil {
		return err
	}

	msgs, err := a.amqpChannel.Consume(q.Name, "", false, false, false, false, nil)
	if err != nil {
		return err
	}

	for msg := range msgs {
		select {
		case <-ctx.Done():
			_ = msg.Nack(false, true)
			a.logger.Info("[EVENT HANDLER] shutting down handler")
			return nil
		default:
			a.logger.Info("[EVENT HANDLER] new event processed", "name", "AccountCreated")
			_ = msg.Ack(false)
		}
	}

	return nil
}

func (a *App) setupRabbitMq() error {
	err := a.amqpChannel.ExchangeDeclare(
		"AccountCreated",
		"fanout",
		true,
		false,
		false,
		false,
		nil,
	)
	if err != nil {
		return err
	}
	a.logger.Debug("AccountCreated exchange declared with success")

	return nil
}

func (a *App) startOutboxPublisher(ctx context.Context) error {
	// Query the oldest 5 events
	rows, err := a.db.QueryContext(ctx, `SELECT id, name, payload, published_at FROM outbox_events ORDER BY published_at DESC LIMIT 5`)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()

	var outboxEvents []OutboxEvent
	for rows.Next() {
		if rows.Err() != nil {
			return err
		}

		var row OutboxEvent
		err = rows.Scan(&row.ID, &row.Name, &row.Payload, &row.PublishedAt)
		if err != nil {
			return err
		}

		outboxEvents = append(outboxEvents, row)
	}

	for _, outboxEvent := range outboxEvents {
		err = a.publishEvent(ctx, outboxEvent.Name, outboxEvent.Payload)
		if err != nil {
			a.logger.Warn("error publishing event from the outbox", "event", outboxEvent, "error", err)
			continue
		}
		a.logger.Debug("event published with success", "event_name", outboxEvent.Name, "payload", string(outboxEvent.Payload))

		// We assume that it's ok to have this event re-published later
		_, err = a.db.ExecContext(ctx, `DELETE FROM outbox_events WHERE id = $1`, outboxEvent.ID)
		if err != nil {
			// Ideally alerts for this kind of warnings will have been set up
			a.logger.Warn("error deleting published event from the outbox", "event", outboxEvent, "error", err)
			continue
		}
	}

	return nil
}

func (a *App) startOutboxPublisherScheduler(ctx context.Context) error {
	ticker := time.NewTicker(time.Second * 5)

	for {
		select {
		case <-ctx.Done():
			a.logger.Info("the scheduler has been stopped via context cancellation")
			return nil
		case <-ticker.C:
			err := a.startOutboxPublisher(ctx)
			if err != nil {
				return err
			}
		}
	}
}
