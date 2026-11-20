# Локальный запуск бэкенда

Из каталога `backend` в PowerShell. Нужны Go с поддержкой версии из `go.mod` и Docker Compose.

```powershell
$env:POSTGRES_PASSWORD = [guid]::NewGuid().ToString('N')
$env:DATABASE_URL = "postgres://company_search:${env:POSTGRES_PASSWORD}@127.0.0.1:5432/company_search?sslmode=disable"
docker compose up -d --wait
go run ./cmd/migrate
go run ./cmd/api
```

При повторном запуске с тем же томом PostgreSQL используй пароль, с которым том был создан. `HTTP_ADDR` задаёт адрес сервера, по умолчанию `:8080`. `sslmode=disable` подходит для локального стенда.

После запуска оба маршрута возвращают `204`. При остановленной PostgreSQL `/readyz` возвращает `503`, а `/healthz` продолжает возвращать `204`.

```powershell
curl.exe -i http://localhost:8080/healthz
curl.exe -i http://localhost:8080/readyz
go test ./...
```
