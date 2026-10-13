<#
  Smoke test for every route of the Geofence API. Works in Windows PowerShell 5.1 and PowerShell 7+.

  Usage (from the project folder, with the API running):
      powershell -ExecutionPolicy Bypass -File .\scripts\smoke-test.ps1
      powershell -ExecutionPolicy Bypass -File .\scripts\smoke-test.ps1 -BaseUrl http://localhost:8080 -ApiKey secret

  It creates its own geofence and vehicle each run, so it can be run repeatedly. Exit code is 0 when
  every check passes, 1 otherwise.
#>
param(
    [string]$BaseUrl = "http://localhost:8080",
    [string]$ApiKey = ""
)

$ErrorActionPreference = "Stop"
$headers = @{}
if ($ApiKey) { $headers["X-API-Key"] = $ApiKey }

$script:passed = 0
$script:failed = 0

function Check([string]$Name, [bool]$Condition) {
    if ($Condition) { Write-Host "  PASS  $Name" -ForegroundColor Green; $script:passed++ }
    else { Write-Host "  FAIL  $Name" -ForegroundColor Red; $script:failed++ }
}

function Call([string]$Method, [string]$Path, $Body = $null) {
    $params = @{ Method = $Method; Uri = "$BaseUrl$Path"; Headers = $headers; ContentType = "application/json" }
    if ($null -ne $Body) { $params.Body = ($Body | ConvertTo-Json -Depth 10 -Compress) }
    Invoke-RestMethod @params
}

# Returns the HTTP status code of a request that is expected to fail (0 if it unexpectedly succeeded).
function CallStatus([string]$Method, [string]$Path, $Body = $null) {
    try { Call $Method $Path $Body | Out-Null; return 0 }
    catch { return [int]$_.Exception.Response.StatusCode }
}

function Iso([int]$MinutesAgo) { (Get-Date).ToUniversalTime().AddMinutes(-$MinutesAgo).ToString("yyyy-MM-ddTHH:mm:ssZ") }

# Every run uses its own random area so zones left by earlier runs never overlap this run's zone.
$rand = New-Object System.Random
$dLat = [math]::Round($rand.NextDouble() * 60 - 30, 4)
$dLon = [math]::Round($rand.NextDouble() * 100 - 50, 4)
# Rounded to 6 decimals so the numbers survive the JSON round trip exactly.
function R([double]$v) { [math]::Round($v, 6) }
$lat0 = R (37.7749 + $dLat); $lat1 = R (37.7849 + $dLat)
$lon0 = R (-122.4194 + $dLon); $lon1 = R (-122.4094 + $dLon)
$insideLat = R (37.7799 + $dLat); $insideLon = R (-122.4144 + $dLon)
$outsideLat = R (37.70 + $dLat); $outsideLon = R (-122.50 + $dLon)
$plate = "WIN-" + $rand.Next(100000, 999999)

Write-Host "Testing $BaseUrl" -ForegroundColor Cyan

Write-Host "`n[health]"
$r = Call GET "/health"
Check "GET /health returns ok" ($r.status -eq "ok")

Write-Host "`n[1] POST /geofences"
$geo = Call POST "/geofences" @{
    name        = "Smoke Restricted Zone"
    description = "created by smoke-test.ps1"
    coordinates = @(@($lat0, $lon0), @($lat1, $lon0), @($lat1, $lon1), @($lat0, $lon1), @($lat0, $lon0))
    category    = "restricted_zone"
}
Check "returns id geo_*" ($geo.id -like "geo_*")
Check "status is active" ($geo.status -eq "active")
Check "has time_ns" ($geo.time_ns -match '^\d+$')

Write-Host "`n[2] GET /geofences"
$list = Call GET "/geofences"
Check "list contains the new geofence" (@($list.geofences | Where-Object { $_.id -eq $geo.id }).Count -eq 1)
$filtered = Call GET "/geofences?category=restricted_zone"
Check "category filter returns only restricted_zone" (@($filtered.geofences | Where-Object { $_.category -ne "restricted_zone" }).Count -eq 0)

Write-Host "`n[3] POST /vehicles"
$veh = Call POST "/vehicles" @{ vehicle_number = $plate; driver_name = "Smoke Driver"; vehicle_type = "truck"; phone = "+911234567890" }
Check "returns id veh_*" ($veh.id -like "veh_*")
Check "vehicle_number echoed" ($veh.vehicle_number -eq $plate)

Write-Host "`n[4] GET /vehicles"
$vlist = Call GET "/vehicles"
Check "list contains the new vehicle" (@($vlist.vehicles | Where-Object { $_.id -eq $veh.id }).Count -eq 1)

Write-Host "`n[7] POST /alerts/configure"
$alert = Call POST "/alerts/configure" @{ geofence_id = $geo.id; event_type = "both" }
Check "returns alert_id alert_*" ($alert.alert_id -like "alert_*")
Check "vehicle_id is null (all vehicles)" ($null -eq $alert.vehicle_id)
$alert2 = Call POST "/alerts/configure" @{ geofence_id = $geo.id; vehicle_id = $veh.id; event_type = "entry" }
Check "vehicle-specific rule created" ($alert2.vehicle_id -eq $veh.id)

Write-Host "`n[8] GET /alerts"
$alerts = Call GET "/alerts?geofence_id=$($geo.id)"
Check "2 rules for the geofence" (@($alerts.alerts).Count -eq 2)
$alertsByVeh = Call GET "/alerts?vehicle_id=$($veh.id)"
Check "1 rule for the vehicle" (@($alertsByVeh.alerts).Count -eq 1)

Write-Host "`n[5] POST /vehicles/location"
$r = Call POST "/vehicles/location" @{ vehicle_id = $veh.id; latitude = $outsideLat; longitude = $outsideLon; timestamp = (Iso 5) }
Check "outside: location_updated and no geofences" ($r.location_updated -eq $true -and @($r.current_geofences).Count -eq 0)
$r = Call POST "/vehicles/location" @{ vehicle_id = $veh.id; latitude = $insideLat; longitude = $insideLon; timestamp = (Iso 4) }
Check "inside: reports the restricted zone" (@($r.current_geofences).Count -eq 1 -and $r.current_geofences[0].geofence_id -eq $geo.id -and $r.current_geofences[0].status -eq "inside")

Write-Host "`n[6] GET /vehicles/location/{vehicle_id}"
$loc = Call GET "/vehicles/location/$($veh.id)"
Check "vehicle_number matches" ($loc.vehicle_number -eq $plate)
Check "current_location has latitude and timestamp" ($loc.current_location.latitude -eq $insideLat -and $loc.current_location.timestamp)
Check "current_geofences has category restricted_zone" ($loc.current_geofences[0].category -eq "restricted_zone")

$r = Call POST "/vehicles/location" @{ vehicle_id = $veh.id; latitude = $outsideLat; longitude = $outsideLon; timestamp = (Iso 3) }
Check "leaving: no geofences" (@($r.current_geofences).Count -eq 0)

Write-Host "`n[9] GET /violations/history"
$h = Call GET "/violations/history?vehicle_id=$($veh.id)&limit=100"
Check "2 events (entry then exit), newest first" ($h.total_count -eq 2 -and $h.violations[0].event_type -eq "exit" -and $h.violations[1].event_type -eq "entry")
$h = Call GET "/violations/history?geofence_id=$($geo.id)"
Check "geofence filter works" ($h.total_count -eq 2)
$start = [uri]::EscapeDataString((Iso 60)); $end = [uri]::EscapeDataString((Iso -60))
$h = Call GET "/violations/history?vehicle_id=$($veh.id)&start_date=$start&end_date=$end"
Check "date range filter works" ($h.total_count -eq 2)
$h = Call GET "/violations/history?vehicle_id=$($veh.id)&limit=1&offset=1"
Check "pagination: 1 row, total_count still 2" (@($h.violations).Count -eq 1 -and $h.total_count -eq 2)

Write-Host "`n[extra] GET /vehicles/locations, GET /alerts/events, DELETE /alerts/{id}"
$all = Call GET "/vehicles/locations"
Check "vehicles/locations includes the vehicle" (@($all.locations | Where-Object { $_.vehicle_id -eq $veh.id }).Count -eq 1)
$ev = Call GET "/alerts/events?limit=10"
Check "alert events were generated (entry + exit)" (@($ev.events | Where-Object { $_.vehicle.vehicle_id -eq $veh.id }).Count -ge 2)
$del = Call DELETE "/alerts/$($alert2.alert_id)"
Check "delete alert rule" ($del.deleted -eq $true)
Check "delete again returns 404" ((CallStatus DELETE "/alerts/$($alert2.alert_id)") -eq 404)

Write-Host "`n[errors]"
Check "invalid category -> 400" ((CallStatus POST "/geofences" @{ name = "x"; category = "nope"; coordinates = @(@(0, 0), @(0, 1), @(1, 1), @(0, 0)) }) -eq 400)
Check "unclosed polygon -> 400" ((CallStatus POST "/geofences" @{ name = "x"; category = "toll_zone"; coordinates = @(@(0, 0), @(0, 1), @(1, 1), @(1, 0)) }) -eq 400)
Check "latitude out of range -> 400" ((CallStatus POST "/geofences" @{ name = "x"; category = "toll_zone"; coordinates = @(@(95, 0), @(0, 0), @(0, 1), @(95, 0)) }) -eq 400)
Check "duplicate vehicle number -> 409" ((CallStatus POST "/vehicles" @{ vehicle_number = $plate; driver_name = "x"; vehicle_type = "car"; phone = "1" }) -eq 409)
Check "missing vehicle fields -> 400" ((CallStatus POST "/vehicles" @{ vehicle_number = "ONLY" }) -eq 400)
Check "location for unknown vehicle -> 404" ((CallStatus POST "/vehicles/location" @{ vehicle_id = "veh_nope"; latitude = 1; longitude = 1; timestamp = (Iso 1) }) -eq 404)
Check "latitude out of range on location -> 400" ((CallStatus POST "/vehicles/location" @{ vehicle_id = $veh.id; latitude = 123; longitude = 1; timestamp = (Iso 1) }) -eq 400)
Check "bad timestamp -> 400" ((CallStatus POST "/vehicles/location" @{ vehicle_id = $veh.id; latitude = 1; longitude = 1; timestamp = "yesterday" }) -eq 400)
Check "unknown vehicle location -> 404" ((CallStatus GET "/vehicles/location/veh_nope") -eq 404)
Check "invalid event_type -> 400" ((CallStatus POST "/alerts/configure" @{ geofence_id = $geo.id; event_type = "sometimes" }) -eq 400)
Check "unknown geofence on alert -> 404" ((CallStatus POST "/alerts/configure" @{ geofence_id = "geo_nope"; event_type = "entry" }) -eq 404)
Check "bad history limit -> 400" ((CallStatus GET "/violations/history?limit=abc") -eq 400)

Write-Host ""
$color = if ($script:failed -eq 0) { "Green" } else { "Red" }
Write-Host "Passed: $($script:passed)   Failed: $($script:failed)" -ForegroundColor $color
if ($script:failed -gt 0) { exit 1 }
