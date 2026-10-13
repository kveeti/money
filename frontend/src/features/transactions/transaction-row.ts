import type { Posting, Transaction } from "../../api/transactions";

export type TransactionRow =
  | {
      kind: "simple";
      category: string;
      amount: number;
      currency: string;
      account?: string;
    }
  | {
      kind: "categories";
      categories: string[];
      amount: number;
      currency: string;
      account: string;
    }
  | {
      kind: "entries";
      groups: { direction: "To" | "From"; names: string[] }[];
      amount: number;
      currency: string;
      account: string;
    }
  | {
      kind: "transfer";
      from: string;
      to: string;
      amount: number;
      currency: string;
      account?: string;
    }
  | {
      kind: "exchange";
      from: string;
      to: string;
      fromAmount: number;
      fromCurrency: string;
      toAmount: number;
      toCurrency: string;
      account?: string;
    }
  | {
      kind: "generic";
      legs: { name: string; amount: number; currency: string }[];
    };

export function toTransactionRow(txn: Transaction): TransactionRow {
  const name = (p: Posting) => p.bucket.name;
  const kind = (p: Posting) => p.bucket.kind;
  const isAccount = (p: Posting) =>
    kind(p) === "asset" || kind(p) === "liability";
  const entryDirection = (p: Posting): "To" | "From" =>
    p.amount >= 0 ? "To" : "From";

  if (txn.transfer && txn.postings.length === 1) {
    const posting = txn.postings[0];
    const counterpartBucket = txn.transfer.counterpart_bucket;
    const counterpartAmount = txn.transfer.counterpart_amount;
    const counterpartCurrency = txn.transfer.counterpart_currency;
    const account =
      txn.transfer.counterpart_occurred_on &&
      txn.transfer.counterpart_occurred_on !== txn.occurred_on
        ? name(posting)
        : undefined;

    if (
      counterpartBucket &&
      counterpartAmount !== undefined &&
      counterpartCurrency
    ) {
      const outgoing =
        txn.transfer.side === "outgoing"
          ? {
              name: name(posting),
              amount: posting.amount,
              currency: posting.currency,
            }
          : {
              name: counterpartBucket.name,
              amount: counterpartAmount,
              currency: counterpartCurrency,
            };
      const incoming =
        txn.transfer.side === "incoming"
          ? {
              name: name(posting),
              amount: posting.amount,
              currency: posting.currency,
            }
          : {
              name: counterpartBucket.name,
              amount: counterpartAmount,
              currency: counterpartCurrency,
            };

      if (outgoing.currency !== incoming.currency) {
        return {
          kind: "exchange",
          from: outgoing.name,
          to: incoming.name,
          fromAmount: outgoing.amount,
          fromCurrency: outgoing.currency,
          toAmount: incoming.amount,
          toCurrency: incoming.currency,
          account,
        };
      }
      return {
        kind: "transfer",
        from: outgoing.name,
        to: incoming.name,
        amount: posting.amount,
        currency: posting.currency,
        account,
      };
    }

    const other = txn.transfer.unmatched ? "Unmatched side" : "Other account";
    return {
      kind: "transfer",
      from: txn.transfer.side === "incoming" ? other : name(posting),
      to: txn.transfer.side === "incoming" ? name(posting) : other,
      amount: posting.amount,
      currency: posting.currency,
    };
  }

  const importedLegs = txn.postings.filter((p) => p.imported);
  const editableLegs = txn.postings.filter((p) => !p.imported);
  const fixedLeg = importedLegs.length === 1 ? importedLegs[0] : undefined;
  if (
    fixedLeg &&
    isAccount(fixedLeg) &&
    editableLegs.length > 1 &&
    editableLegs.every((p) => p.currency === fixedLeg.currency)
  ) {
    const isCategorySplit =
      editableLegs.every(
        (p) =>
          kind(p) === "expense" || kind(p) === "income" || kind(p) === "person",
      ) &&
      editableLegs.every((p) => p.amount * fixedLeg.amount < 0) &&
      editableLegs.reduce((sum, p) => sum + p.amount, 0) === -fixedLeg.amount;
    if (isCategorySplit) {
      return {
        kind: "categories",
        categories: editableLegs.map(name),
        amount: fixedLeg.amount,
        currency: fixedLeg.currency,
        account: name(fixedLeg),
      };
    }

    const directions = [...new Set(editableLegs.map(entryDirection))];
    return {
      kind: "entries",
      groups: directions.map((direction) => ({
        direction,
        names: editableLegs
          .filter((p) => entryDirection(p) === direction)
          .map(name),
      })),
      amount: fixedLeg.amount,
      currency: fixedLeg.currency,
      account: name(fixedLeg),
    };
  }

  if (txn.postings.length === 2) {
    const category = txn.postings.find(
      (p) =>
        kind(p) === "expense" || kind(p) === "income" || kind(p) === "person",
    );
    const other = txn.postings.find((p) => p !== category);
    if (
      category &&
      other &&
      isAccount(other) &&
      category.currency === other.currency
    ) {
      return {
        kind: "simple",
        category: name(category),
        amount: -category.amount,
        currency: category.currency,
        account: name(other),
      };
    }
  }

  if (
    txn.postings.length === 2 &&
    txn.postings.every(isAccount) &&
    txn.postings[0].currency === txn.postings[1].currency
  ) {
    const neg = txn.postings.find((p) => p.amount < 0)!;
    const pos = txn.postings.find((p) => p.amount > 0)!;
    return {
      kind: "transfer",
      from: name(neg),
      to: name(pos),
      amount: Math.abs(neg.amount),
      currency: neg.currency,
    };
  }

  const clearingLegs = txn.postings.filter((p) => kind(p) === "clearing");
  const moneyLegs = txn.postings.filter((p) => kind(p) !== "clearing");
  if (
    clearingLegs.length === 2 &&
    moneyLegs.length === 2 &&
    moneyLegs.every(isAccount) &&
    moneyLegs[0].currency !== moneyLegs[1].currency &&
    moneyLegs.every((money) =>
      clearingLegs.some(
        (clearing) =>
          clearing.currency === money.currency &&
          clearing.amount === -money.amount,
      ),
    )
  ) {
    const neg = moneyLegs.find((p) => p.amount < 0);
    const pos = moneyLegs.find((p) => p.amount > 0);
    if (neg && pos) {
      return {
        kind: "exchange",
        from: name(neg),
        to: name(pos),
        fromAmount: neg.amount,
        fromCurrency: neg.currency,
        toAmount: pos.amount,
        toCurrency: pos.currency,
      };
    }
  }

  return {
    kind: "generic",
    legs: txn.postings.map((p) => ({
      name: name(p),
      amount: p.amount,
      currency: p.currency,
    })),
  };
}
