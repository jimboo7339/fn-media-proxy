param(
    [string]$Base = "http://127.0.0.1:18096",
    [string]$User = "demo",
    [string]$Password = "123456"
)

$ErrorActionPreference = "Continue"
$results = New-Object System.Collections.ArrayList

function Check($name, $ok, $detail) {
    [void]$script:results.Add([pscustomobject]@{ Check = $name; OK = $ok; Detail = $detail })
    $mark = if ($ok) { "PASS" } else { "FAIL" }
    Write-Host ("[{0}] {1}  {2}" -f $mark, $name, $detail)
}

function Q($s) { return "?" + $s }

# 1 system info public
try {
    $r = Invoke-RestMethod -Uri "$Base/System/Info/Public" -TimeoutSec 10
    Check "System/Info/Public" ($null -ne $r.ServerName) ("ServerName=" + $r.ServerName)
} catch { Check "System/Info/Public" $false $_.Exception.Message }

# 2 login
$token = $null
$uid = $null
try {
    $body = @{ Username = $User; Pw = $Password } | ConvertTo-Json
    $r = Invoke-RestMethod -Uri "$Base/Users/AuthenticateByName" -Method Post -Body $body -ContentType "application/json" -TimeoutSec 20
    $token = $r.AccessToken
    $uid = $r.User.Id
    Check "AuthenticateByName" ($null -ne $token) ("user=" + $r.User.Name + " uid=" + $uid)
} catch { Check "AuthenticateByName" $false $_.Exception.Message }

if (-not $token) {
    $results | Format-Table -AutoSize
    exit 1
}
$auth = @{ "X-Emby-Token" = $token }

# 3 users me
try {
    $r = Invoke-RestMethod -Uri "$Base/Users/Me" -Headers $auth -TimeoutSec 10
    Check "Users/Me" ($r.Name -eq $User) ("Name=" + $r.Name)
} catch { Check "Users/Me" $false $_.Exception.Message }

# 4 views
$libId = $null
try {
    $r = Invoke-RestMethod -Uri "$Base/Users/$uid/Views" -Headers $auth -TimeoutSec 15
    $libId = $r.Items[0].Id
    Check "Views" ($r.TotalRecordCount -gt 0) ("libs=" + $r.TotalRecordCount + " first=" + $r.Items[0].Name + " ct=" + $r.Items[0].CollectionType)
} catch { Check "Views" $false $_.Exception.Message }

if (-not $libId) {
    $results | Format-Table -AutoSize
    exit 1
}

# 5 items in lib
$firstItem = $null
try {
    $u = "$Base/Items" + (Q "ParentId=$libId") + "&Limit=5&api_key=$token"
    $r = Invoke-RestMethod -Uri $u -Headers $auth -TimeoutSec 90
    $firstItem = $r.Items[0]
    Check "Items(lib)" ($r.TotalRecordCount -gt 0) ("total=" + $r.TotalRecordCount + " first=" + $firstItem.Name + " type=" + $firstItem.Type)
} catch { Check "Items(lib)" $false $_.Exception.Message }

# 6 item detail
if ($firstItem) {
    try {
        $u = "$Base/Items/" + $firstItem.Id + (Q "api_key=$token")
        $r = Invoke-RestMethod -Uri $u -Headers $auth -TimeoutSec 20
        $tags = ($r.ImageTags.PSObject.Properties.Name -join ",")
        Check "Items/{id}" ($null -ne $r.Id) ("Name=" + $r.Name + " Type=" + $r.Type + " Year=" + $r.ProductionYear + " ImageTags=" + $tags)
    } catch { Check "Items/{id}" $false $_.Exception.Message }
}

# 7 episodes
if ($firstItem -and $firstItem.Type -eq "Series") {
    try {
        $u = "$Base/Shows/" + $firstItem.Id + "/Episodes" + (Q "api_key=$token")
        $r = Invoke-RestMethod -Uri $u -Headers $auth -TimeoutSec 90
        $ep = $r.Items[0]
        Check "Shows/Episodes" ($r.TotalRecordCount -gt 0) ("eps=" + $r.TotalRecordCount + " first=" + $ep.Name + " S" + $ep.ParentIndexNumber + "E" + $ep.IndexNumber)
    } catch { Check "Shows/Episodes" $false $_.Exception.Message }
}

# 8 playbackinfo
$pbOk = $false
if ($firstItem) {
    try {
        $u = "$Base/Items/" + $firstItem.Id + "/PlaybackInfo" + (Q "api_key=$token")
        $r = Invoke-RestMethod -Uri $u -Method Post -Body "{}" -ContentType "application/json" -Headers $auth -TimeoutSec 90
        $src = $r.MediaSources[0]
        $vs = $src.MediaStreams | Where-Object { $_.Type -eq "Video" } | Select-Object -First 1
        $ac = ($src.MediaStreams | Where-Object { $_.Type -eq "Audio" }).Count
        $pbOk = ($null -ne $src.Path)
        Check "PlaybackInfo" $pbOk ("container=" + $src.Container + " streams=" + $src.MediaStreams.Count + " video=" + $vs.Codec + " " + $vs.Width + "x" + $vs.Height + " audio=" + $ac)
    } catch { Check "PlaybackInfo" $false $_.Exception.Message }
}

# 9 302 direct link via curl
if ($pbOk) {
    $u = "$Base/Videos/" + $firstItem.Id + "/stream" + (Q "Static=true&api_key=$token")
    $out = & curl.exe -s -o NUL -w "%{http_code}|%{redirect_url}" $u 2>&1
    $parts = "$out" -split "\|"
    $code = $parts[0]
    $loc = ""
    if ($parts.Count -gt 1) { $loc = $parts[1] }
    $locShort = ""
    if ($loc.Length -gt 70) { $locShort = $loc.Substring(0, 70) } else { $locShort = $loc }
    Check "302 direct" (($code -eq "302") -and ($loc.Length -gt 10)) ("status=" + $code + " loc=" + $locShort)
}

# 10 image
if ($firstItem) {
    try {
        $u = "$Base/Items/" + $firstItem.Id + "/Images/Primary" + (Q "api_key=$token")
        $resp = Invoke-WebRequest -Uri $u -Headers $auth -UseBasicParsing -TimeoutSec 20
        Check "Images/Primary" ($resp.StatusCode -eq 200) ("status=" + $resp.StatusCode + " bytes=" + $resp.RawContentLength)
    } catch { Check "Images/Primary" $false $_.Exception.Message }
}

# 11 progress (position 0, does not alter real progress)
if ($firstItem) {
    try {
        $body = @{ ItemId = $firstItem.Id; PlaySessionId = "smoke"; PositionTicks = 0; IsPaused = $true } | ConvertTo-Json
        $u = "$Base/Sessions/Playing/Progress" + (Q "api_key=$token")
        $resp = Invoke-WebRequest -Uri $u -Method Post -Body $body -ContentType "application/json" -Headers $auth -UseBasicParsing -TimeoutSec 20
        Check "Sessions/Progress" ($resp.StatusCode -eq 204) ("status=" + $resp.StatusCode)
    } catch { Check "Sessions/Progress" $false $_.Exception.Message }
}

# 12 emby prefix
try {
    $resp = Invoke-WebRequest -Uri "$Base/emby/System/Info/Public" -UseBasicParsing -TimeoutSec 10
    Check "emby prefix" ($resp.StatusCode -eq 200) ("status=" + $resp.StatusCode)
} catch { Check "emby prefix" $false $_.Exception.Message }

# 13 lowercase path
try {
    $resp = Invoke-WebRequest -Uri "$Base/emby/system/info/public" -UseBasicParsing -TimeoutSec 10
    Check "lowercase path" ($resp.StatusCode -eq 200) ("status=" + $resp.StatusCode)
} catch { Check "lowercase path" $false $_.Exception.Message }

Write-Host ""
Write-Host "======== summary ========"
$results | Format-Table AutoSize
$pass = ($results | Where-Object { $_.OK }).Count
Write-Host ("PASS " + $pass + "/" + $results.Count)
