#Requires -Version 7.4
$ErrorActionPreference = 'Stop'
$PSNativeCommandUseErrorActionPreference = $true
$container = "company-search-test-$([guid]::NewGuid().ToString('N'))"
$password = [guid]::NewGuid().ToString('N')
$settings = @{}
$started = $false

Push-Location $PSScriptRoot
try {
    $null = docker run --detach --rm --name $container --publish 127.0.0.1::5432 --env "POSTGRES_PASSWORD=$password" --health-cmd 'pg_isready -h 127.0.0.1 -U postgres' --health-interval 1s postgres:17-alpine
    $started = $true
    $deadline = [DateTime]::UtcNow.AddSeconds(60)
    do {
        $health = docker inspect --format '{{.State.Health.Status}}' $container
        if ($health -eq 'healthy') { break }
        if ([DateTime]::UtcNow -ge $deadline) { throw 'Test PostgreSQL did not become ready' }
        Start-Sleep -Seconds 1
    } while ($true)

    $port = (docker port $container '5432/tcp') -replace '^127\.0\.0\.1:', ''
    docker exec $container psql -U postgres -v ON_ERROR_STOP=1 -c "CREATE ROLE company_api LOGIN PASSWORD '$password'; CREATE ROLE company_importer LOGIN PASSWORD '$password';" | Out-Null

    function Set-TestEnv($name, $value) {
        if (!$settings.ContainsKey($name)) {
            $settings[$name] = [Environment]::GetEnvironmentVariable($name)
        }
        [Environment]::SetEnvironmentVariable($name, $value)
    }

    $databases = @{ ACCOUNTS = 'stage14_test'; CHAT = 'stage15_test'; STAGE16 = 'stage16_test' }
    foreach ($stage in $databases.Keys) {
        $db = $databases[$stage]
        docker exec $container createdb -U postgres $db
        $suffix = "@127.0.0.1:$port/${db}?sslmode=disable"
        $admin = "postgres://postgres:${password}$suffix"
        Set-TestEnv DATABASE_URL $admin
        go run ./cmd/migrate
        Get-Content -Raw db-roles.sql | docker exec -i $container psql -U postgres -d $db | Out-Null
        Set-TestEnv "COMPANY_${stage}_TEST_ADMIN_URL" $admin
        Set-TestEnv "COMPANY_${stage}_TEST_API_URL" "postgres://company_api:${password}$suffix"
        if ($stage -eq 'ACCOUNTS') {
            Set-TestEnv COMPANY_MIGRATE_TEST_DATABASE_URL $admin
            Set-TestEnv COMPANY_IMPORT_TEST_DATABASE_URL "postgres://company_importer:${password}$suffix"
        }
    }

    $names = 'TestApplyPostgreSQLIdempotent|TestCorrectedRelease|TestSearchLookupLeavesConnectionAvailable|TestAccountSessionsAndExternalQuota|TestPasswordResetPostgreSQL|TestChatHTTP|TestChatCompletionsTokenBudget|TestStage16AccountHTTP|TestStage16ChatManagementHTTP'
    $events = go test -count=1 -json -run "^($names)$" ./migrations ./cmd/import ./internal/company ./internal/httpapi ./internal/agent |
        ForEach-Object {
            $event = $_ | ConvertFrom-Json
            if ($event.Output) { Write-Host $event.Output -NoNewline }
            $event
        }
    $passed = @($events | Where-Object Action -eq 'pass' | ForEach-Object Test)
    foreach ($name in ($names -split '\|')) {
        if ($name -notin $passed) { throw "Required test did not pass: $name" }
    }
} finally {
    try {
        if ($started) { docker rm -f $container | Out-Null }
    } finally {
        foreach ($name in $settings.Keys) {
            [Environment]::SetEnvironmentVariable($name, $settings[$name])
        }
        Pop-Location
    }
}
