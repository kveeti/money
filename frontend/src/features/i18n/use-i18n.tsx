import { type ReactNode, useMemo } from "react";

import { useCurrenciesQuery } from "../../api/currencies";
import { createContext } from "../../lib/create-context";

const [useContext, context] = createContext<ReturnType<typeof useI18nValue>>();

type Currencies = Array<{ code: string; exponent: number }>;

export const useI18n = useContext;
export function I18n({ children }: { children: ReactNode }) {
  const currencies = useCurrenciesQuery();
  const value = useI18nValue({
    currencies: currencies.data ?? [],
    isLoading: currencies.isLoading,
    isError: currencies.isError,
  });
  return <context.Provider value={value}>{children}</context.Provider>;
}

function useI18nValue(props: {
  currencies: Currencies;
  isLoading: boolean;
  isError: boolean;
}) {
  const locale = undefined;
  const timeZone = useMemo(
    () => new Intl.DateTimeFormat(locale).resolvedOptions().timeZone,
    [locale],
  );
  const hourCycle: 12 | 24 = 24;
  const currentYear = new Date().getFullYear();

  const shortDateFormatter = useMemo(
    () =>
      new Intl.DateTimeFormat(locale, {
        weekday: "short",
        month: "short",
        day: "numeric",
        timeZone,
      }),
    [locale, timeZone],
  );

  const longDateFormatter = useMemo(
    () =>
      new Intl.DateTimeFormat(locale, {
        month: "short",
        day: "numeric",
        year: "numeric",
        timeZone,
      }),
    [locale, timeZone],
  );

  const shortDateOnlyFormatter = useMemo(
    () =>
      new Intl.DateTimeFormat(locale, {
        weekday: "short",
        month: "short",
        day: "numeric",
        timeZone: "UTC",
      }),
    [locale],
  );

  const longDateOnlyFormatter = useMemo(
    () =>
      new Intl.DateTimeFormat(locale, {
        month: "short",
        day: "numeric",
        year: "numeric",
        timeZone: "UTC",
      }),
    [locale],
  );

  const longDateTimeFormatter = useMemo(
    () =>
      new Intl.DateTimeFormat(locale, {
        dateStyle: "long",
        timeStyle: "short",
        timeZone,
      }),
    [locale, timeZone],
  );

  const monthFormatter = useMemo(
    () =>
      new Intl.DateTimeFormat(locale, {
        month: "long",
        timeZone,
      }),
    [locale, timeZone],
  );

  const monthYearFormatter = useMemo(
    () =>
      new Intl.DateTimeFormat(locale, {
        month: "long",
        year: "numeric",
        timeZone,
      }),
    [locale, timeZone],
  );

  const percentFormatter = useMemo(
    () =>
      new Intl.NumberFormat(locale, {
        style: "percent",
        maximumFractionDigits: 0,
        signDisplay: "always",
      }),
    [locale],
  );

  const formattersByCurrency = useMemo(() => {
    const formatters: Record<
      string,
      {
        amount: (amount: number) => string;
        signedAmount: (amount: number) => string;
        wholeAmount: (amount: number) => string;
      }
    > = {};

    for (const { code, exponent } of props.currencies) {
      if (formatters[code])
        throw new Error(`Formatter for ${code} already defined`);
      const amount = new Intl.NumberFormat(locale, {
        style: "currency",
        currency: code,
      });
      const signedAmount = new Intl.NumberFormat(locale, {
        style: "currency",
        currency: code,
        signDisplay: "always",
      });
      const wholeAmount = new Intl.NumberFormat(locale, {
        style: "currency",
        currency: code,
        maximumFractionDigits: 0,
      });
      const divisor = 10 ** exponent;
      formatters[code] = {
        amount: (value) => amount.format(value / divisor),
        signedAmount: (value) => signedAmount.format(value / divisor),
        wholeAmount: (value) => wholeAmount.format(value / divisor),
      };
    }

    return formatters;
  }, [props.currencies, locale]);

  return {
    isLoading: props.isLoading,
    isError: props.isError,
    f: {
      amount: (amount: number, isoCurrency: string) =>
        formattersByCurrency[isoCurrency].amount(amount),
      signedAmount: (amount: number, isoCurrency: string) =>
        formattersByCurrency[isoCurrency].signedAmount(amount),
      wholeAmount: (amount: number, isoCurrency: string) =>
        formattersByCurrency[isoCurrency].wholeAmount(amount),
      percent: percentFormatter.format,
      shortDate: shortDateFormatter.format,
      longDate: longDateFormatter.format,
      dateOnly: (value: string) => {
        const date = new Date(`${value}T00:00:00Z`);
        return date.getUTCFullYear() === currentYear
          ? shortDateOnlyFormatter.format(date)
          : longDateOnlyFormatter.format(date);
      },
      longDateTime: longDateTimeFormatter.format,
      month: monthFormatter.format,
      monthYear: monthYearFormatter.format,
    },
    hourCycle,
    timeZone,
  };
}
