$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $PSScriptRoot
$voNet = Get-Content (Join-Path $root "proto/agent/v1/agent.proto")
$agent = Get-Content (Join-Path $root "../VoNet-Agent/proto/agent/v1/agent.proto")
$normalize = {
    param($lines)
    $lines | ForEach-Object { $_ -replace '^option go_package = .*$', 'option go_package = "<normalized>";' }
}
$left = & $normalize $voNet
$right = & $normalize $agent
if (($left -join "`n") -ne ($right -join "`n")) {
    throw "VoNet and VoNet-Agent agent.proto wire contracts differ"
}
