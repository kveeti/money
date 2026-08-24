import { Button } from "../../ui/button/button";

export default function LoginPage() {
  const demoEnabled =
    new URLSearchParams(window.location.search).get("demo") === "1";

  return (
    <main className="flex min-h-dvh items-center justify-center bg-gray-125 px-4 py-10">
      <div className="w-full max-w-sm rounded-[2.75rem] border border-popover-border bg-form p-8 shadow-[0_2px_8px_oklch(10%_0.01_60deg_/_3%)]">
        <h1 className="mb-2 text-2xl font-semibold text-gray-950">
          Track your money
        </h1>
        <p className="mb-8 text-base text-gray-700">
          Import transactions, organize them, and see where your money goes.
        </p>

        <div className="flex flex-col gap-3">
          <form action="/api/v1/auth/login" method="get">
            <Button type="submit" className="w-full">
              Sign in
            </Button>
          </form>

          {demoEnabled && (
            <form
              method="post"
              action="/api/v1/auth/demo"
              className="rounded-3xl border border-demo-border bg-demo-surface p-3"
            >
              <Button type="submit" variant="demo" className="w-full">
                Try the demo
              </Button>
              <p className="mt-2 text-center text-sm text-demo-fg">
                Includes sample data and is deleted after 30 minutes.
              </p>
            </form>
          )}
        </div>
      </div>
    </main>
  );
}
