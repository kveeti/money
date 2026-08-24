Personal expense tracker
- React, Go, PostgreSQL
- Stores data using double-entry accounting
- Transaction categorization
- Stats over time with comparisons e.g. this month vs last month
  - Shown in chosen home currency
- Match specific types of transactions that don't lose money, ignored in stats
  - Currency exchange
  - Transfers between own accounts
- Split one transaction into multiple categories/persons, e.g.
  - Restaurant transaction of $60 that consists of your meal and another persons meal split into
    - Category: Restaurants $30 (your split)
    - Person: Alice $30 (other person's split)
- Tag transactions, e.g.
  - Expenses abroad or in a specific destination `paris-2026`
  - Specific project like renovation, bike, car etc. `canyon-endurance-2026`
- Automatic bank syncing using [Enable Banking](https://enablebanking.com/) with PSD2
- [More screenshots](#more-screenshots)

<table>
  <tr>
    <td><img src=".readme-assets/transactions-light.webp" alt="Transactions page in the light theme" width="460"></td>
    <td><img src=".readme-assets/transactions-dark.webp" alt="Transactions page in the dark theme" width="460"></td>
  </tr>
</table>

## Development

The Nix shell starts PostgreSQL and sets the local URLs and mock OIDC credentials:

```bash
nix develop
make devi
```

`make devi` starts the mock identity provider, backend, and frontend. The shell prints their URLs and ports.

## Configuration

`env.new.example` is a minimal deployment template. The backend requires these settings:

| Variable | Purpose |
| --- | --- |
| `BACKEND_URL` | Public backend origin. |
| `DB_URL` | PostgreSQL connection URL. |
| `OIDC_ISSUER` | OIDC provider URL. |
| `OIDC_CLIENT_ID` | OIDC client ID. |
| `OIDC_CLIENT_SECRET` | OIDC client secret. |

Optional settings:

| Variable | Default | Purpose |
| --- | --- | --- |
| `IS_PROD` | `0` | Set to `1` to enable production mode. |
| `PORT` | `8000` | Backend listen port. |
| `FRONT_URL` | `BACKEND_URL` | Separate frontend origin. Leave unset when the backend serves the frontend. |
| `OIDC_REDIRECT_URL` | `BACKEND_URL/api/v1/auth/callback` | Override only for a proxy or path rewrite. |
| `DEV_VITE_URL` | unset | Vite origin proxied by the backend during development. |
| `IMPORT_STORE` | `postgres` | Import file storage: `postgres` or `disk`. |
| `IMPORT_DIR` | unset | Import directory when `IMPORT_STORE=disk`. |
| `DISABLE_RATE_SYNC` | `0` | Set to `1` to stop exchange-rate syncing. |
| `ENABLEBANKING_APP_ID` | unset | Enable Banking application ID. Set with `ENABLEBANKING_PRIVATE_KEY`. |
| `ENABLEBANKING_PRIVATE_KEY` | unset | Path to the Enable Banking private key. Set with `ENABLEBANKING_APP_ID`. |
| `ENABLEBANKING_API_ORIGIN` | `https://api.enablebanking.com` | Enable Banking API origin. Register `BACKEND_URL/api/v1/enablebanking/callback` as the callback. |

## More screenshots

<table>
  <tr><th align="left">Monthly stats</th></tr>
  <tr><td><img src=".readme-assets/stats.webp" alt="Monthly stats comparing income and expense categories with the previous month" width="460"></td></tr>
</table>

<table>
  <tr><th align="left">Inbox</th></tr>
  <tr><td><img src=".readme-assets/inbox.webp" alt="Inbox with uncategorized transactions and category actions" width="460"></td></tr>
</table>

<table>
  <tr><th align="left">Transaction filters</th></tr>
  <tr><td><img src=".readme-assets/filters.webp" alt="Transaction filters for amount, category, tags, date range, and account" width="460"></td></tr>
</table>

<table>
  <tr><th align="left">Transaction details</th></tr>
  <tr><td><img src=".readme-assets/transaction-details.webp" alt="Transaction details with account, memo, category, and tags" width="460"></td></tr>
</table>

<table>
  <tr><th align="left">Demo imports</th></tr>
  <tr><td><img src=".readme-assets/import-demo.webp" alt="Demo import page with CSV upload and sample CSV controls" width="460"></td></tr>
</table>

