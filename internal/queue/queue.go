package queue

import (
	"database/sql"
	"time"
)

type Product struct {
	ID                int
	ItemID            int64
	Title             string
	Price             float64
	Discount          int
	Commission        float64
	ImageURL          string
	OfferLink         string
	Source            string
	Status            string
	TelegramMessageID int
	CreatedAt         time.Time
	SentAt            *time.Time
}

type Queue struct {
	db *sql.DB
}

func NewQueue(db *sql.DB) *Queue {
	return &Queue{db: db}
}

func Migrate(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS queue (
		id                  INTEGER PRIMARY KEY AUTOINCREMENT,
		item_id             INTEGER,
		title               TEXT NOT NULL,
		price               REAL NOT NULL,
		discount            INTEGER,
		commission          REAL NOT NULL,
		image_url           TEXT,
		offer_link          TEXT NOT NULL UNIQUE,
		source              TEXT NOT NULL DEFAULT 'shopee',
		status              TEXT NOT NULL DEFAULT 'pending_review',
		telegram_message_id INTEGER,
		created_at          DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		sent_at             DATETIME
	)`)
	if err != nil {
		return err
	}

	// Migrações não-destrutivas para bancos existentes.
	for _, col := range []string{
		`ALTER TABLE queue ADD COLUMN telegram_message_id INTEGER`,
		`ALTER TABLE queue ADD COLUMN item_id INTEGER`,
	} {
		if _, err := db.Exec(col); err != nil && !isSQLiteAlreadyExists(err) {
			return err
		}
	}

	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS seen_items (
		item_id       INTEGER PRIMARY KEY,
		first_seen_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`)
	return err
}

// isSQLiteAlreadyExists detecta o erro "duplicate column name" do SQLite.
func isSQLiteAlreadyExists(err error) bool {
	return err != nil && (contains(err.Error(), "duplicate column name") || contains(err.Error(), "already exists"))
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && containsStr(s, sub))
}

func containsStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// Enqueue insere o produto na fila com status pending_review.
// Retorna (true, nil) se inserido, (false, nil) se já existia.
func (q *Queue) Enqueue(p Product) (bool, error) {
	stmt, err := q.db.Prepare(`INSERT OR IGNORE INTO queue
		(item_id, title, price, discount, commission, image_url, offer_link, source, status)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'pending_review')`)
	if err != nil {
		return false, err
	}
	defer stmt.Close()

	source := p.Source
	if source == "" {
		source = "shopee"
	}

	var itemID any
	if p.ItemID != 0 {
		itemID = p.ItemID
	}

	result, err := stmt.Exec(itemID, p.Title, p.Price, p.Discount, p.Commission, p.ImageURL, p.OfferLink, source)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	return rows > 0, err
}

// IsSeenItem retorna true se o itemId já foi visto em algum ciclo anterior.
func (q *Queue) IsSeenItem(itemID int64) (bool, error) {
	var count int
	err := q.db.QueryRow(`SELECT COUNT(*) FROM seen_items WHERE item_id = ?`, itemID).Scan(&count)
	return count > 0, err
}

// MarkSeenItem registra o itemId em seen_items. É idempotente (INSERT OR IGNORE).
func (q *Queue) MarkSeenItem(itemID int64) error {
	_, err := q.db.Exec(`INSERT OR IGNORE INTO seen_items (item_id) VALUES (?)`, itemID)
	return err
}

func (q *Queue) GetByOfferLink(offerLink string) (*Product, error) {
	stmt, err := q.db.Prepare(`SELECT id, item_id, title, price, discount, commission, image_url, offer_link, source, status, telegram_message_id, created_at, sent_at
		FROM queue WHERE offer_link = ?`)
	if err != nil {
		return nil, err
	}
	defer stmt.Close()

	return scanProduct(stmt.QueryRow(offerLink))
}

func (q *Queue) Dequeue() (*Product, error) {
	stmt, err := q.db.Prepare(`SELECT id, item_id, title, price, discount, commission, image_url, offer_link, source, status, telegram_message_id, created_at, sent_at
		FROM queue WHERE status = 'pending' ORDER BY created_at ASC LIMIT 1`)
	if err != nil {
		return nil, err
	}
	defer stmt.Close()

	return scanProduct(stmt.QueryRow())
}

func (q *Queue) GetByID(id int) (*Product, error) {
	stmt, err := q.db.Prepare(`SELECT id, item_id, title, price, discount, commission, image_url, offer_link, source, status, telegram_message_id, created_at, sent_at
		FROM queue WHERE id = ?`)
	if err != nil {
		return nil, err
	}
	defer stmt.Close()

	return scanProduct(stmt.QueryRow(id))
}

func scanProduct(row *sql.Row) (*Product, error) {
	var p Product
	var itemID sql.NullInt64
	var sentAt sql.NullTime
	var telegramMessageID sql.NullInt64
	err := row.Scan(
		&p.ID, &itemID, &p.Title, &p.Price, &p.Discount, &p.Commission,
		&p.ImageURL, &p.OfferLink, &p.Source, &p.Status, &telegramMessageID, &p.CreatedAt, &sentAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if itemID.Valid {
		p.ItemID = itemID.Int64
	}
	if sentAt.Valid {
		p.SentAt = &sentAt.Time
	}
	if telegramMessageID.Valid {
		p.TelegramMessageID = int(telegramMessageID.Int64)
	}
	return &p, nil
}

func (q *Queue) MarkSent(id int) error {
	stmt, err := q.db.Prepare(`UPDATE queue SET status = 'sent', sent_at = CURRENT_TIMESTAMP WHERE id = ?`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	_, err = stmt.Exec(id)
	return err
}

func (q *Queue) MarkFailed(id int) error {
	stmt, err := q.db.Prepare(`UPDATE queue SET status = 'failed' WHERE id = ?`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	_, err = stmt.Exec(id)
	return err
}

func (q *Queue) MarkPending(id int) error {
	stmt, err := q.db.Prepare(`UPDATE queue SET status = 'pending' WHERE id = ?`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	_, err = stmt.Exec(id)
	return err
}

func (q *Queue) MarkPendingReview(id int) error {
	stmt, err := q.db.Prepare(`UPDATE queue SET status = 'pending_review' WHERE id = ?`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	_, err = stmt.Exec(id)
	return err
}

func (q *Queue) MarkRejected(id int) error {
	stmt, err := q.db.Prepare(`UPDATE queue SET status = 'rejected' WHERE id = ?`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	_, err = stmt.Exec(id)
	return err
}

func (q *Queue) SetTelegramMessageID(id int, messageID int) error {
	stmt, err := q.db.Prepare(`UPDATE queue SET telegram_message_id = ? WHERE id = ?`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	_, err = stmt.Exec(messageID, id)
	return err
}

func (q *Queue) SetImageURL(id int, imageURL string) error {
	stmt, err := q.db.Prepare(`UPDATE queue SET image_url = ? WHERE id = ?`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	_, err = stmt.Exec(imageURL, id)
	return err
}

func (q *Queue) UpdateProductData(id int, title string, price float64, discount int) error {
	stmt, err := q.db.Prepare(`UPDATE queue SET title = ?, price = ?, discount = ? WHERE id = ?`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	_, err = stmt.Exec(title, price, discount, id)
	return err
}

func (q *Queue) CountPending() (int, error) {
	var count int
	err := q.db.QueryRow(`SELECT COUNT(*) FROM queue WHERE status = 'pending'`).Scan(&count)
	return count, err
}

func (q *Queue) ListPending() ([]Product, error) {
	rows, err := q.db.Query(`SELECT id, item_id, title, price, discount, commission, image_url, offer_link, source, status, telegram_message_id, created_at, sent_at
		FROM queue WHERE status = 'pending' ORDER BY created_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var products []Product
	for rows.Next() {
		var p Product
		var itemID sql.NullInt64
		var sentAt sql.NullTime
		var telegramMessageID sql.NullInt64
		if err := rows.Scan(
			&p.ID, &itemID, &p.Title, &p.Price, &p.Discount, &p.Commission,
			&p.ImageURL, &p.OfferLink, &p.Source, &p.Status, &telegramMessageID, &p.CreatedAt, &sentAt,
		); err != nil {
			return nil, err
		}
		if itemID.Valid {
			p.ItemID = itemID.Int64
		}
		if sentAt.Valid {
			p.SentAt = &sentAt.Time
		}
		if telegramMessageID.Valid {
			p.TelegramMessageID = int(telegramMessageID.Int64)
		}
		products = append(products, p)
	}
	return products, rows.Err()
}
