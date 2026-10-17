alter table sessions
    add column access_token text,
    add column refresh_token text,
    add column refresh_retry_after timestamptz;

-- Existing sessions have no provider tokens and cannot use the new auth path.
delete from sessions;
