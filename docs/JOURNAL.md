# Журнал этапов

## Этап 10. Внутренний слой инструментов

Реализация начата по разрешению владельца. Этап 9 принят.

### Результат

Пять инструментов вызывают общие операции чтения в `internal/company`. HTTP использует те же операции и сохраняет формат ответа из OpenAPI. Исполнитель проверяет JSON, идентификаторы, курсор и сочетания налоговых фильтров до обращения к PostgreSQL. `user_id` передаёт вызывающий код. На этапе 10 персональные квоты отсутствуют.

Результат инструмента содержит `data` или `error`. В частичном ответе сохраняются доступные наблюдения, покрытие, предупреждения и пагинация. `ambiguous_identity` возвращает кандидатов в `error.candidates`. В ответах с наблюдениями `source_ref` разрешается в `data.sources`. Дата публикации ФНС хранится в источнике, а время получения DaData хранится в `retrieved_at` наблюдения. При неизвестной дате состояния DaData передаёт `as_of: null`.

Генератор сопоставляет имена пяти инструментов с `operationId` OpenAPI и проверяет аргументы по таблице этапа 9. Схемы доступны вызывающему коду через `tools.Schemas()`. Вызов выполняет `tools.Executor.Execute(ctx, userID, toolName, args)`, где `args` имеет тип `json.RawMessage`.

### Проверки 21.11.2026

- `go run ./internal/tools/schemagen -check` прошла. В изолированных копиях снижение максимума `search.limit` со 100 до 99 выявило устаревший JSON, а замена `auto` на `manual` в таблице этапа 9 выявила расхождение с OpenAPI.
- `go test -count=1 -v ./...` прошла без пропусков на PostgreSQL 17 с набором `fixture-v1`. Проверены пять инструментов, валидация до чтения, источники и даты, опубликованный ноль, две страницы поиска, `next_offset`, отсутствие записи, неприменимость, незагруженный выпуск и выбор регистрации.
- `TestPublishedCompanyAPI`, `TestPublishedEmploymentAndRegimes` и `TestPublishedMoneyTaxes` прошли на той же базе. Отдельная проба на сохранённой выборке ФНС этапа 6 дошла до проверки карантина, но в выборке не было конфликтов. В `fixture-v1` сценарий карантина проверен на 15 тестовых записях.
- `go vet ./...` и `git diff --check` прошли. DaData подменяли в тестах. Полные выпуски ФНС, рабочая база и живой API DaData в этом этапе не проверялись.

### Ручная приёмка

1. Из каталога `backend` выполни `go run ./internal/tools/schemagen` и `go run ./internal/tools/schemagen -check`. Сверь [пять схем](../backend/internal/tools/schemas.json) с таблицей [операций этапа 9](ARCHITECTURE.md#f-будущая-интеграция-с-ии).
2. Укажи в `DATABASE_URL` пустую тестовую PostgreSQL и выполни команды ниже. Загрузка SQL предназначена для пустой базы со свежими миграциями.
3. Сверь проверки `TestExecutePostgreSQL`. Поиск по ИНН `500100732259` возвращает две регистрации. Запрос карточки по тому же ИНН требует выбора. В тесте задолженности для `6612025806` ожидаются `221695.72` на `2026-10-01`, источник `debtam` и следующая страница.

```powershell
go run ./cmd/migrate
psql $env:DATABASE_URL -v ON_ERROR_STOP=1 -f internal/tools/testdata/stage10.sql
$env:COMPANY_TOOLS_TEST_DATABASE_URL = $env:DATABASE_URL
$env:COMPANY_API_TEST_DATABASE_URL = $env:DATABASE_URL
$env:COMPANY_API_STAGE6_TEST_DATABASE_URL = $env:DATABASE_URL
$env:COMPANY_API_STAGE7_TEST_DATABASE_URL = $env:DATABASE_URL
go test -count=1 -v ./...
go vet ./...
```

**Решение владельца.** Этап 10 принят владельцем 21.11.2026.
