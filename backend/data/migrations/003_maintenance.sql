alter table import_batches
    add column claim_id uuid,
    add column claim_expires_at timestamptz,
    add check ((claim_id is null) = (claim_expires_at is null));

create index idx_import_batches_expired_claim
    on import_batches(claim_expires_at)
    where status in ('processing', 'syncing');

delete from import_files stored
where not exists (
    select 1 from import_batches batch where batch.id = stored.batch_id
);

alter table import_files
    add constraint import_files_batch_id_fkey
    foreign key (batch_id) references import_batches(id) on delete cascade
    deferrable initially deferred;
