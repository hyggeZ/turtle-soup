# 使用 Edge DevTools 截取固定视口，便于复查静态稿的桌面与手机排版。
param([string]$Only = '')
$ErrorActionPreference = 'Stop'
[Console]::InputEncoding = [Console]::OutputEncoding = [System.Text.UTF8Encoding]::new($false)
$OutputEncoding = [Console]::OutputEncoding
$base = Split-Path -Parent $MyInvocation.MyCommand.Path
$edge = 'C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe'
$profile = Join-Path $env:TEMP 'turtle-admin-ui-render'
$port = 9341
$html = ([IO.Path]::GetFullPath((Join-Path $base 'index.html'))).Replace('\', '/')
$startUrl = "file:///${html}?page=home"
$edgeArgs = @(
  '--headless=new', '--disable-gpu', '--hide-scrollbars', '--no-first-run',
  '--force-device-scale-factor=1', '--remote-allow-origins=*',
  "--remote-debugging-port=$port", "--user-data-dir=$profile", '--window-size=1440,991', $startUrl
)
$process = Start-Process -FilePath $edge -ArgumentList $edgeArgs -PassThru -WindowStyle Hidden
$socket = $null

function Receive-Message($ws) {
  $buffer = New-Object byte[] 65536
  $result = New-Object System.Text.StringBuilder
  do {
    $segment = [ArraySegment[byte]]::new($buffer)
    $part = $ws.ReceiveAsync($segment, [Threading.CancellationToken]::None).GetAwaiter().GetResult()
    if ($part.MessageType -eq [Net.WebSockets.WebSocketMessageType]::Close) { throw 'DevTools 连接已关闭。' }
    [void]$result.Append([Text.Encoding]::UTF8.GetString($buffer, 0, $part.Count))
  } while (-not $part.EndOfMessage)
  return $result.ToString() | ConvertFrom-Json
}

function Invoke-Cdp($ws, $id, $method, $params) {
  $message = @{ id = $id; method = $method; params = $params } | ConvertTo-Json -Depth 10 -Compress
  $bytes = [Text.Encoding]::UTF8.GetBytes($message)
  $segment = [ArraySegment[byte]]::new($bytes)
  [void]$ws.SendAsync($segment, [Net.WebSockets.WebSocketMessageType]::Text, $true, [Threading.CancellationToken]::None).GetAwaiter().GetResult()
  while ($true) {
    $received = Receive-Message $ws
    if ($received.id -eq $id) {
      if ($received.error) { throw "$method : $($received.error.message)" }
      return $received.result
    }
  }
}

try {
  $target = $null
  for ($i = 0; $i -lt 40; $i++) {
    try {
      $tabs = Invoke-RestMethod "http://127.0.0.1:$port/json"
      $target = $tabs | Where-Object { $_.type -eq 'page' -and $_.url -eq $startUrl } | Select-Object -First 1
      if ($target) { break }
    } catch { }
    Start-Sleep -Milliseconds 200
  }
  if (-not $target) { throw '未找到设计稿页面。' }
  $socket = [Net.WebSockets.ClientWebSocket]::new()
  [void]$socket.ConnectAsync([Uri]$target.webSocketDebuggerUrl, [Threading.CancellationToken]::None).GetAwaiter().GetResult()
  [void](Invoke-Cdp $socket 1 'Page.enable' @{})
  $shots = @(
    @('home',1440,900,'01-home.png'),
    @('accounts',1440,900,'02-accounts.png'),
    @('create',1440,900,'03-create-account.png'),
    @('password',1440,900,'04-change-password.png'),
    @('soups',1440,900,'05-soups.png'),
    @('logs',1440,900,'06-logs.png'),
    @('trace',1440,900,'07-trace.png'),
    @('home',390,844,'08-home-mobile.png'),
    @('create',390,844,'09-create-mobile.png'),
    @('accounts',390,844,'10-accounts-mobile.png'),
    @('logs',1440,1320,'11-logs-full.png'),
    @('login',1440,900,'12-login.png'),
    @('login',390,844,'13-login-mobile.png'),
    @('ai-trace',1440,900,'14-ai-trace.png')
  )
  $id = 2
  foreach ($shot in $shots) {
    $page, $width, $height, $name = $shot
    if ($Only -and $Only -ne $page -and $Only -ne $name) { continue }
    [void](Invoke-Cdp $socket $id 'Emulation.setDeviceMetricsOverride' @{ width=$width; height=$height; deviceScaleFactor=1; mobile=($width -lt 700) })
    $id++
    [void](Invoke-Cdp $socket $id 'Page.navigate' @{ url="file:///${html}?page=$page" })
    $id++
    Start-Sleep -Milliseconds 650
    $clip = @{ x=0; y=0; width=$width; height=$height; scale=1 }
    $result = Invoke-Cdp $socket $id 'Page.captureScreenshot' @{ format='png'; fromSurface=$true; captureBeyondViewport=$false; clip=$clip }
    $id++
    $path = Join-Path $base $name
    [IO.File]::WriteAllBytes($path, [Convert]::FromBase64String($result.data))
    Write-Output $path
  }
} finally {
  if ($socket) { $socket.Dispose() }
  if ($process -and -not $process.HasExited) { Stop-Process -Id $process.Id -Force }
}
