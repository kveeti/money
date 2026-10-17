alter table sessions
    add column tokens_need_verification boolean not null default false,
    add column pending_id_token text;
