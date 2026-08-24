package data

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

type Users struct {
	*sql.DB
}

func NewUsers(s *sql.DB) *Users {
	return &Users{DB: s}
}

type User struct {
	ID            string
	Subject       string
	Issuer        string
	Email         string
	HomeCurrency  string
	CreatedAt     time.Time
	IsDemo        bool
	DemoExpiresAt *time.Time
}

type userQueryRower interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// CreateUser inserts a new user and both required system buckets atomically.
func (d *Data) CreateUser(ctx context.Context, user User) error {
	return createUser(ctx, d.db, user)
}

func createUser(ctx context.Context, db userQueryRower, user User) error {
	var users, buckets, audits int
	err := db.QueryRowContext(ctx, `
		with inserted_user as (
			insert into users (
				id, subject, issuer, email, created_at, is_demo, demo_expires_at
			)
			values ($1, $2, $3, $4, $5, $6, $7)
			returning id
		), system_buckets(id, kind, name) as (
			values
				(uuidv7(), 'transit', 'Transit'),
				(uuidv7(), 'fx_conversion', 'FX conversion')
		), inserted_buckets as (
			insert into buckets (
				id, owner_user_id, kind, name, hidden, created_at
			)
			select bucket.id, owner.id, bucket.kind, bucket.name, true, now()
			from inserted_user owner
			cross join system_buckets bucket
			returning id
		), audited_user as (
			insert into audit_logs (
				id, actor_user_id, table_name, row_id, operation, before, created_at
			)
			select uuidv7(), id, 'users', id, 'insert', null, now()
			from inserted_user
			returning id
		), audited_buckets as (
			insert into audit_logs (
				id, actor_user_id, table_name, row_id, operation, before, created_at
			)
			select uuidv7(), $1, 'buckets', id, 'insert', null, now()
			from inserted_buckets
			returning id
		)
		select
			(select count(*) from inserted_user),
			(select count(*) from inserted_buckets),
			(select count(*) from audited_user) +
				(select count(*) from audited_buckets)
	`,
		user.ID,
		user.Subject,
		user.Issuer,
		user.Email,
		user.CreatedAt.UTC(),
		user.IsDemo,
		user.DemoExpiresAt,
	).Scan(&users, &buckets, &audits)
	if err != nil {
		return err
	}
	if users != 1 || buckets != 2 || audits != 3 {
		return fmt.Errorf("create user wrote %d users, %d buckets, and %d audits", users, buckets, audits)
	}
	return nil
}

func (s *Users) GetUserByID(ctx context.Context, userID string) (*User, error) {
	return s.scanUser(s.QueryRowContext(ctx, `
		select id, subject, issuer, email, home_currency, created_at,
		       is_demo, demo_expires_at
		from users
		where id = $1
		limit 1
	`, userID))
}

// GetUserBySubject looks up a user by their OIDC issuer + subject identifier.
func (s *Users) GetUserBySubject(ctx context.Context, issuer, subject string) (*User, error) {
	return s.scanUser(s.QueryRowContext(ctx, `
		select id, subject, issuer, email, home_currency, created_at,
		       is_demo, demo_expires_at
		from users
		where issuer = $1
		  and subject = $2
		limit 1
	`, issuer, subject))
}

func (s *Users) scanUser(row *sql.Row) (*User, error) {
	var user User

	err := row.Scan(
		&user.ID, &user.Subject, &user.Issuer, &user.Email,
		&user.HomeCurrency, &user.CreatedAt, &user.IsDemo, &user.DemoExpiresAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	return &user, nil
}
