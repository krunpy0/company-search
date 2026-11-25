This is a [Next.js](https://nextjs.org) project bootstrapped with [`create-next-app`](https://nextjs.org/docs/app/api-reference/cli/create-next-app).

## Go API и сессия

Для входа и регистрации запусти [Go API с PostgreSQL](../backend/README.md), затем фронтенд. Сервер Next.js по умолчанию обращается к `http://127.0.0.1:8080`. Другой адрес Go API задаётся серверной переменной `COMPANY_API_URL`. За прокси задай `COMPANY_APP_ORIGIN` равной внешнему origin фронтенда, включая схему и порт при необходимости, без завершающего слеша.

Для отправки сообщений включи `CHAT_ENABLED=true` в окружении Go API, задай `OPENAI_BASE_URL` и `OPENAI_MODEL`, затем перезапусти Go API. Настройки модели описаны в [README бэкенда](../backend/README.md#api-чата).

После изменения [OpenAPI](../backend/openapi.yaml) выполни из `frontend/`:

```bash
npm run api:generate
```

Команда обновляет `src/lib/api.d.ts`. Серверный клиент использует эти типы для работы с Go API.

## Getting Started

First, run the development server:

```bash
npm run dev
# or
yarn dev
# or
pnpm dev
# or
bun dev
```

Open [http://localhost:3000](http://localhost:3000) with your browser to see the result.

You can start editing the page by modifying `app/page.tsx`. The page auto-updates as you edit the file.

This project uses [`next/font`](https://nextjs.org/docs/app/building-your-application/optimizing/fonts) to automatically optimize and load [Geist](https://vercel.com/font), a new font family for Vercel.

## Learn More

To learn more about Next.js, take a look at the following resources:

- [Next.js Documentation](https://nextjs.org/docs) - learn about Next.js features and API.
- [Learn Next.js](https://nextjs.org/learn) - an interactive Next.js tutorial.

You can check out [the Next.js GitHub repository](https://github.com/vercel/next.js) - your feedback and contributions are welcome!

## Deploy on Vercel

The easiest way to deploy your Next.js app is to use the [Vercel Platform](https://vercel.com/new?utm_medium=default-template&filter=next.js&utm_source=create-next-app&utm_campaign=create-next-app-readme) from the creators of Next.js.

Check out our [Next.js deployment documentation](https://nextjs.org/docs/app/building-your-application/deploying) for more details.
