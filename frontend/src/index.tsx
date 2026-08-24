import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { Redirect, Route, Router, Switch } from "wouter";

import { ConnectionsRoute } from "./features/connections/connections-route";
import DesignPage from "./features/design/design-page";
import { I18n } from "./features/i18n/use-i18n";
import { ImportDropProvider } from "./features/imports/import-drop";
import ImportReportPage from "./features/imports/import-report-page";
import ImportsPage from "./features/imports/imports-page";
import InboxPage from "./features/inbox/inbox-page";
import { Layout } from "./features/layout";
import StatsPage from "./features/stats/stats-page";
import TransactionDetailPage from "./features/transactions/transaction-detail-page";
import TransactionsPage from "./features/transactions/transactions-page";

import "./styles.css";

const queryClient = new QueryClient();

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <I18n>
        <Router>
          <ImportDropProvider>
            <Layout>
              <Switch>
                <Route path="/inbox" component={InboxPage} />
                <Route
                  path="/transactions/:id"
                  component={TransactionDetailPage}
                />
                <Route path="/transactions" component={TransactionsPage} />
                <Route path="/imports" component={ImportsPage} />
                <Route path="/imports/:id" component={ImportReportPage} />
                <Route path="/connections" component={ConnectionsRoute} />
                <Route path="/stats" component={StatsPage} />
                <Route path="/design" component={DesignPage} />
                <Route>
                  <Redirect to="/transactions" />
                </Route>
              </Switch>
            </Layout>
          </ImportDropProvider>
        </Router>
      </I18n>
    </QueryClientProvider>
  </StrictMode>,
);
