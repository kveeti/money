create table demo_rate_limits (
    client_ip inet primary key,
    minute_started_at timestamptz not null,
    minute_count integer not null check (minute_count > 0),
    hour_started_at timestamptz not null,
    hour_count integer not null check (hour_count > 0),
    updated_at timestamptz not null
);

create index idx_demo_rate_limits_updated_at
    on demo_rate_limits(updated_at);
