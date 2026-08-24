package data

import (
	"context"
	"database/sql"
	"fmt"
)

// AllowDemoCreation atomically applies the per-IP demo creation limits.
func (d *Data) AllowDemoCreation(ctx context.Context, clientIP string, perMinute, perHour int) (bool, error) {
	var allowed bool
	err := d.db.QueryRowContext(ctx, `
		insert into demo_rate_limits (
			client_ip, minute_started_at, minute_count,
			hour_started_at, hour_count, updated_at
		)
		values (
			$1::inet, date_trunc('minute', now()), 1,
			date_trunc('hour', now()), 1, now()
		)
		on conflict (client_ip) do update
		set minute_started_at = date_trunc('minute', now()),
		    minute_count = case
		      when demo_rate_limits.minute_started_at = date_trunc('minute', now())
		      then demo_rate_limits.minute_count + 1
		      else 1
		    end,
		    hour_started_at = date_trunc('hour', now()),
		    hour_count = case
		      when demo_rate_limits.hour_started_at = date_trunc('hour', now())
		      then demo_rate_limits.hour_count + 1
		      else 1
		    end,
		    updated_at = now()
		where (
			case
			  when demo_rate_limits.minute_started_at = date_trunc('minute', now())
			  then demo_rate_limits.minute_count
			  else 0
			end
		) < $2
		and (
			case
			  when demo_rate_limits.hour_started_at = date_trunc('hour', now())
			  then demo_rate_limits.hour_count
			  else 0
			end
		) < $3
		returning true
	`, clientIP, perMinute, perHour).Scan(&allowed)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return allowed, err
}

// CreateDemoUser creates a short-lived user and a ready-to-browse Finnish-style ledger.
func (d *Data) CreateDemoUser(ctx context.Context, user User) error {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if err := createUser(ctx, tx, user); err != nil {
		return err
	}

	var buckets, transactions, postings, tags int
	err = tx.QueryRowContext(ctx, `
		with bucket_input(kind, name) as (
			values
				('asset', 'Checking'),
				('liability', 'Credit card'),
				('expense', 'Housing'),
				('expense', 'Groceries'),
				('expense', 'Restaurants'),
				('expense', 'Transport'),
				('expense', 'Shopping'),
				('expense', 'Entertainment'),
				('expense', 'Utilities'),
				('expense', 'Health'),
				('expense', 'Travel'),
				('income', 'Salary')
		), inserted_buckets as (
			insert into buckets (
				id, owner_user_id, kind, name, hidden, created_at
			)
			select uuidv7(), $1, kind, name, false, now()
			from bucket_input
			returning id, name
		), demo_days(days_ago) as (
			select generate_series(1, 90)
		), demo_input(
			days_ago, counterparty, description, account_amount,
			category, account, tag
		) as (
			select
				days_ago,
				case days_ago % 3
					when 0 then 'Robert''s Coffee'
					when 1 then 'Fazer Café'
					else 'Café Regatta'
				end,
				'Coffee',
				-(390 + (days_ago % 7) * 55)::bigint,
				'Restaurants', 'Credit card', null::text
			from demo_days
			where days_ago % 4 = 0

			union all
			select
				days_ago,
				case days_ago % 3
					when 0 then 'K-Market'
					when 1 then 'S-Market'
					else 'Prisma'
				end,
				'Groceries',
				-(2800 + (days_ago % 6) * 735)::bigint,
				'Groceries', 'Checking', null::text
			from demo_days
			where days_ago % 5 = 1

			union all
			select
				days_ago, 'HSL',
				case when days_ago % 2 = 0 then 'Single ticket' else 'Travel card' end,
				case when days_ago % 2 = 0 then -310::bigint else -720::bigint end,
				'Transport', 'Checking', null::text
			from demo_days
			where days_ago % 6 = 2

			union all
			select
				days_ago,
				case days_ago % 4
					when 0 then 'Hesburger'
					when 1 then 'Kotipizza'
					when 2 then 'Ravintola Nolla'
					else 'Wolt'
				end,
				'Dinner',
				-(1800 + (days_ago % 5) * 620)::bigint,
				'Restaurants', 'Credit card', null::text
			from demo_days
			where days_ago % 8 = 3

			union all
			select
				days_ago, 'Lounasravintola Factory', 'Lunch',
				-(1190 + (days_ago % 3) * 100)::bigint,
				'Restaurants', 'Checking', null::text
			from demo_days
			where days_ago % 10 = 4

			union all
			select
				days_ago,
				case when days_ago % 2 = 0 then 'Neste' else 'ABC' end,
				'Fuel',
				-(6200 + (days_ago % 5) * 430)::bigint,
				'Transport', 'Credit card', null::text
			from demo_days
			where days_ago % 16 = 6

			union all
			select
				days_ago,
				case days_ago % 3
					when 0 then 'Verkkokauppa.com'
					when 1 then 'Stockmann'
					else 'Marimekko'
				end,
				'Household purchase',
				-(4500 + (days_ago % 7) * 1250)::bigint,
				'Shopping', 'Credit card', null::text
			from demo_days
			where days_ago % 19 = 8

			union all
			select
				days_ago, 'Yliopiston Apteekki', 'Pharmacy',
				-(1450 + (days_ago % 4) * 610)::bigint,
				'Health', 'Checking', null::text
			from demo_days
			where days_ago % 23 = 11

			union all
			select *
			from (values
				(2,  'Pohjola Design Oy', 'Salary',              326500::bigint, 'Salary',        'Checking',    'recurring'),
				(32, 'Pohjola Design Oy', 'Salary',              326500::bigint, 'Salary',        'Checking',    'recurring'),
				(62, 'Pohjola Design Oy', 'Salary',              326500::bigint, 'Salary',        'Checking',    'recurring'),
				(6,  'Kotikatu Asunnot',  'Rent',               -112000::bigint, 'Housing',       'Checking',    'recurring'),
				(36, 'Kotikatu Asunnot',  'Rent',               -112000::bigint, 'Housing',       'Checking',    'recurring'),
				(66, 'Kotikatu Asunnot',  'Rent',               -112000::bigint, 'Housing',       'Checking',    'recurring'),
				(9,  'Helen',              'Electricity',          -4680::bigint, 'Utilities',     'Checking',    'recurring'),
				(39, 'Helen',              'Electricity',          -5230::bigint, 'Utilities',     'Checking',    'recurring'),
				(69, 'Helen',              'Electricity',          -4910::bigint, 'Utilities',     'Checking',    'recurring'),
				(10, 'Elisa',              'Mobile plan',          -2990::bigint, 'Utilities',     'Checking',    'recurring'),
				(40, 'Elisa',              'Mobile plan',          -2990::bigint, 'Utilities',     'Checking',    'recurring'),
				(70, 'Elisa',              'Mobile plan',          -2990::bigint, 'Utilities',     'Checking',    'recurring'),
				(12, 'Spotify',            'Music subscription',   -1199::bigint, 'Entertainment', 'Credit card', 'recurring'),
				(42, 'Spotify',            'Music subscription',   -1199::bigint, 'Entertainment', 'Credit card', 'recurring'),
				(72, 'Spotify',            'Music subscription',   -1199::bigint, 'Entertainment', 'Credit card', 'recurring'),
				(13, 'Netflix',            'Streaming subscription', -1399::bigint, 'Entertainment', 'Credit card', 'recurring'),
				(43, 'Netflix',            'Streaming subscription', -1399::bigint, 'Entertainment', 'Credit card', 'recurring'),
				(73, 'Netflix',            'Streaming subscription', -1399::bigint, 'Entertainment', 'Credit card', 'recurring'),
				(26, 'VR',                 'Train to Tampere',      -3490::bigint, 'Travel',        'Credit card', 'tampere-weekend'),
				(26, 'Scandic Tampere',    'Hotel',                -12900::bigint, 'Travel',        'Credit card', 'tampere-weekend'),
				(26, 'Ravinteli Bertha',   'Dinner',                -7850::bigint, 'Restaurants',   'Credit card', 'tampere-weekend'),
				(27, 'Museokeskus Vapriikki', 'Museum',             -1500::bigint, 'Entertainment', 'Credit card', 'tampere-weekend'),
				(27, 'Pyynikin Munkkikahvila', 'Coffee and doughnuts', -1320::bigint, 'Restaurants', 'Credit card', 'tampere-weekend'),
				(17, 'Verkkokauppa.com',   'Returned headphones',   6990::bigint, 'Shopping',       'Credit card', null)
			) fixed(days_ago, counterparty, description, account_amount, category, account, tag)
		), prepared as materialized (
			select
				uuidv7() as transaction_id,
				uuidv7() as account_posting_id,
				uuidv7() as category_posting_id,
				current_date - input.days_ago as occurred_on,
				input.counterparty,
				input.description,
				input.account_amount,
				input.tag,
				account.id as account_id,
				category.id as category_id
			from demo_input input
			join inserted_buckets account on account.name = input.account
			join inserted_buckets category on category.name = input.category
		), inserted_transactions as (
			insert into transactions (
				id, owner_user_id, occurred_on, counterparty, description, memo, created_at
			)
			select transaction_id, $1, occurred_on, counterparty, description, '', now()
			from prepared
			returning id
		), inserted_postings as (
			insert into postings (
				id, transaction_id, bucket_id, amount, currency, created_at, updated_at
			)
			select account_posting_id, transaction_id, account_id, account_amount, 'EUR', now(), now()
			from prepared
			union all
			select category_posting_id, transaction_id, category_id, -account_amount, 'EUR', now(), now()
			from prepared
			returning id
		), inserted_tags as (
			insert into posting_tags (id, posting_id, tag, created_at)
			select uuidv7(), category_posting_id, tag, now()
			from prepared
			where tag is not null
			returning id
		)
		select
			(select count(*) from inserted_buckets),
			(select count(*) from inserted_transactions),
			(select count(*) from inserted_postings),
			(select count(*) from inserted_tags)
	`, user.ID).Scan(&buckets, &transactions, &postings, &tags)
	if err != nil {
		return err
	}
	if buckets != 12 || transactions < 100 || postings != transactions*2 || tags < 20 {
		return fmt.Errorf(
			"create demo wrote %d buckets, %d transactions, %d postings, and %d tags",
			buckets, transactions, postings, tags,
		)
	}

	return tx.Commit()
}
