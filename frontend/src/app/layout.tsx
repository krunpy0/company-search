import type { Metadata } from "next";
import { Onest } from "next/font/google";
import { productName } from "./product";
import "./globals.css";

const onest = Onest({ subsets: ["latin", "cyrillic"] });

export const metadata: Metadata = {
  title: `${productName} — аналитика бизнеса`,
  description: "Анализ компаний и проверка контрагентов по официальным данным.",
};

export default function RootLayout({ children }: LayoutProps<"/">) {
  return (
    <html lang="ru" className={onest.className}>
      <body>{children}</body>
    </html>
  );
}
