import { forwardRef, type ButtonHTMLAttributes } from "react";

export type ButtonVariant =
  | "primary"
  | "outline"
  | "ghost"
  | "destructive"
  | "demo"
  | "demoOutline";
interface Props extends ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: ButtonVariant;
}
const variants = {
  primary:
    "border-success-solid bg-gradient-to-b from-success-solid-hover to-success-solid text-white shadow-[inset_0_1px_0_rgb(255_255_255_/_0.2),0_2px_4px_rgb(0_0_0_/_0.15),0_1px_2px_rgb(0_0_0_/_0.1)] hover:not-disabled:from-success-solid-hover hover:not-disabled:to-success-solid-hover focus-visible:outline-success-solid focus-visible:outline-offset-2",
  outline:
    "border-gray-350 bg-transparent text-gray-900 hover:not-disabled:bg-gray-250",
  ghost: "bg-transparent text-gray-900 hover:not-disabled:bg-gray-200",
  destructive:
    "border-danger-border bg-danger-surface text-danger-fg hover:not-disabled:bg-danger-surface-hover focus-visible:outline-danger-focus focus-visible:outline-offset-2",
  demo: "border-demo-solid bg-demo-solid text-white shadow-[inset_0_1px_0_rgb(255_255_255_/_0.18),0_2px_4px_rgb(0_0_0_/_0.12)] hover:not-disabled:border-demo-solid-hover hover:not-disabled:bg-demo-solid-hover focus-visible:outline-demo-focus focus-visible:outline-offset-2",
  demoOutline:
    "border-demo-border bg-demo-surface text-demo-fg hover:not-disabled:bg-demo-surface-hover focus-visible:outline-demo-focus focus-visible:outline-offset-2",
};
export const Button = forwardRef<HTMLButtonElement, Props>(function Button(
  { variant = "primary", className, ...rest },
  ref,
) {
  return (
    <button
      {...rest}
      ref={ref}
      className={`inline-flex h-9 items-center justify-center gap-2 whitespace-nowrap rounded-xl border border-transparent px-4 font-[inherit] font-medium outline-2 outline-transparent outline-offset-[-1px] select-none focus-visible:outline-gray-500 disabled:cursor-not-allowed disabled:opacity-50 ${variants[variant]} ${className ?? ""}`}
    />
  );
});
