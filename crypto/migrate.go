package crypto

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// MigrateColumn encrypts all non-empty, non-encrypted values in a column.
// It processes rows in batches to avoid memory issues and uses a transaction
// per batch to ensure consistency.
//
// A value is considered already encrypted if it starts with "enc:".
// Returns the total number of rows encrypted.
func MigrateColumn(
	ctx context.Context,
	db *pgxpool.Pool,
	enc *Encryptor,
	table string,
	column string,
	batchSize int,
) (int, error) {
	if !enc.IsEnabled() {
		return 0, nil
	}

	if err := validateIdentifier(table); err != nil {
		return 0, fmt.Errorf("crypto: invalid table name: %w", err)
	}
	if err := validateIdentifier(column); err != nil {
		return 0, fmt.Errorf("crypto: invalid column name: %w", err)
	}

	if batchSize <= 0 {
		batchSize = 1000
	}

	totalEncrypted := 0

	for {
		count, err := migrateBatch(ctx, db, enc, table, column, batchSize)
		if err != nil {
			return totalEncrypted, fmt.Errorf("crypto: migration batch failed after %d rows: %w",
				totalEncrypted, err)
		}

		totalEncrypted += count

		if count < batchSize {
			break
		}
	}

	return totalEncrypted, nil
}

// migrateBatch processes a single batch of rows, encrypting plaintext values.
// Returns the number of rows encrypted in this batch.
func migrateBatch(
	ctx context.Context,
	db *pgxpool.Pool,
	enc *Encryptor,
	table string,
	column string,
	batchSize int,
) (int, error) {
	tx, err := db.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	// Select rows where the column is not null and not already encrypted.
	// Using parameterized LIMIT; table/column are validated identifiers.
	query := fmt.Sprintf(
		`SELECT id, %q FROM %q WHERE %q IS NOT NULL AND %q NOT LIKE 'enc:%%' LIMIT $1`,
		column, table, column, column,
	)

	rows, err := tx.Query(ctx, query, batchSize)
	if err != nil {
		return 0, fmt.Errorf("query rows: %w", err)
	}
	defer rows.Close()

	type row struct {
		id    int64
		value string
	}

	var batch []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.value); err != nil {
			return 0, fmt.Errorf("scan row: %w", err)
		}
		batch = append(batch, r)
	}

	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("iterate rows: %w", err)
	}

	if len(batch) == 0 {
		return 0, nil
	}

	updateQuery := fmt.Sprintf(
		`UPDATE %q SET %q = $1 WHERE id = $2`,
		table, column,
	)

	for _, r := range batch {
		encrypted, err := enc.Encrypt(r.value)
		if err != nil {
			return 0, fmt.Errorf("encrypt row id=%d: %w", r.id, err)
		}

		if _, err := tx.Exec(ctx, updateQuery, encrypted, r.id); err != nil {
			return 0, fmt.Errorf("update row id=%d: %w", r.id, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit transaction: %w", err)
	}

	return len(batch), nil
}

// validateIdentifier performs basic validation on SQL identifiers (table/column names)
// to prevent SQL injection. Only allows alphanumeric characters and underscores.
func validateIdentifier(name string) error {
	if name == "" {
		return fmt.Errorf("identifier must not be empty")
	}

	for _, c := range name {
		if !isIdentifierChar(c) {
			return fmt.Errorf("identifier %q contains invalid character %q", name, c)
		}
	}

	// Must not be a SQL keyword that could cause issues
	upper := strings.ToUpper(name)
	reserved := []string{"SELECT", "INSERT", "UPDATE", "DELETE", "DROP", "ALTER", "CREATE", "TRUNCATE"}
	for _, kw := range reserved {
		if upper == kw {
			return fmt.Errorf("identifier %q is a reserved SQL keyword", name)
		}
	}

	return nil
}

// isIdentifierChar returns true if the character is valid in a SQL identifier.
func isIdentifierChar(c rune) bool {
	return (c >= 'a' && c <= 'z') ||
		(c >= 'A' && c <= 'Z') ||
		(c >= '0' && c <= '9') ||
		c == '_'
}
