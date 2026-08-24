alter table import_batches
    add column parse_error_count integer not null default 0
    check (parse_error_count >= 0);
