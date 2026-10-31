package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/kristianwind/verdande/internal/auth"
)

// OAuthClient is a program that registered itself to connect to an account: in
// practice an MCP client such as Claude. It holds no secret — see migration 0038.
type OAuthClient struct {
	ID           string
	Name         string
	RedirectURIs []string
	CreatedAt    time.Time
}

// unusedClientAge is how long a registration may sit without ever being
// approved before it is cleared. Registration is open to anybody who can reach
// the instance, so without this the table grows with every abandoned attempt.
const unusedClientAge = 24 * time.Hour

func (db *DB) CreateOAuthClient(ctx context.Context, name string, redirectURIs []string) (*OAuthClient, error) {
	uris, err := json.Marshal(redirectURIs)
	if err != nil {
		return nil, err
	}
	c := &OAuthClient{ID: NewID(), Name: name, RedirectURIs: redirectURIs, CreatedAt: time.Now().UTC()}

	// Swept on the way in, like the socket tickets: the table is bounded by a
	// day of asking, and nothing has to remember to run a job for it.
	_, _ = db.ExecContext(ctx,
		`DELETE FROM oauth_clients WHERE created_at < ?
		 AND NOT EXISTS (SELECT 1 FROM api_tokens WHERE oauth_client_id = oauth_clients.id)`,
		time.Now().Add(-unusedClientAge).Unix())

	_, err = db.ExecContext(ctx,
		`INSERT INTO oauth_clients (id, name, redirect_uris, created_at) VALUES (?, ?, ?, ?)`,
		c.ID, c.Name, string(uris), c.CreatedAt.Unix())
	if err != nil {
		return nil, err
	}
	return c, nil
}

func (db *DB) OAuthClient(ctx context.Context, id string) (*OAuthClient, error) {
	var c OAuthClient
	var uris string
	var created int64
	err := db.QueryRowContext(ctx,
		`SELECT id, name, redirect_uris, created_at FROM oauth_clients WHERE id = ?`, id).
		Scan(&c.ID, &c.Name, &uris, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(uris), &c.RedirectURIs); err != nil {
		return nil, err
	}
	c.CreatedAt = time.Unix(created, 0).UTC()
	return &c, nil
}

// CreateOAuthToken mints the API token an approval hands to a client.
//
// Any earlier token this person gave the same client is deleted in the same
// transaction. A client that connects again — after a reinstall, or because
// somebody pressed "connect" twice — then has one entry on the tokens page
// rather than a growing column of identical ones, each still valid.
func (db *DB) CreateOAuthToken(ctx context.Context, userID string, client *OAuthClient) (string, error) {
	token, prefix, err := auth.NewAPIToken()
	if err != nil {
		return "", err
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx,
		`DELETE FROM api_tokens WHERE user_id = ? AND oauth_client_id = ?`, userID, client.ID); err != nil {
		return "", err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO api_tokens (id, user_id, name, token_hash, prefix, created_at, oauth_client_id)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		NewID(), userID, client.Name, auth.HashToken(token), prefix, time.Now().Unix(), client.ID); err != nil {
		return "", err
	}
	return token, tx.Commit()
}
