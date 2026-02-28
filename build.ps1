$root = Split-Path -Parent $MyInvocation.MyCommand.Path
$env:GOCACHE = Join-Path $root '.gocache'
$env:GOMODCACHE = Join-Path $root '.gomodcache'

New-Item -ItemType Directory -Force $env:GOCACHE, $env:GOMODCACHE | Out-Null

Push-Location $root
try {
  go run .
} finally {
  Pop-Location
}
