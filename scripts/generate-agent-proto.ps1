$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $PSScriptRoot
Push-Location $root
try {
    protoc --go_out=. --go-grpc_out=. `
        --go_opt=module=vonet --go-grpc_opt=module=vonet `
        proto/agent/v1/agent.proto
}
finally {
    Pop-Location
}
