alter table users
    add column is_demo boolean not null default false,
    add column demo_expires_at timestamptz,
    add check (
        (is_demo and demo_expires_at is not null)
        or (not is_demo and demo_expires_at is null)
    );

create index idx_users_expired_demo
    on users(demo_expires_at)
    where is_demo;
