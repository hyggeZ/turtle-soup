# 用 Edge DevTools 把静态页面导出为可审阅的 PNG，不访问业务接口。
$ErrorActionPreference = 'Stop'
[Console]::InputEncoding = [Console]::OutputEncoding = [System.Text.UTF8Encoding]::new($false)
$OutputEncoding = [Console]::OutputEncoding

$root = Split-Path -Parent $MyInvocation.MyCommand.Path
$edge = 'C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe'
$port = 9368
$profile = Join-Path $env:TEMP ("turtle-soup-ui-play-render-" + $PID)
$baseUrl = 'file:///' + (($root -replace '\\', '/') + '/index.html')
$outDir = Join-Path $root 'screens'
New-Item -ItemType Directory -Force -Path $outDir | Out-Null

$args = @('--headless=new','--disable-gpu','--hide-scrollbars','--no-first-run','--force-device-scale-factor=1','--remote-allow-origins=*',"--remote-debugging-port=$port","--user-data-dir=$profile",'--window-size=1600,1050',"${baseUrl}?view=lobby")
$process = Start-Process -FilePath $edge -ArgumentList $args -WindowStyle Hidden -PassThru
$socket = $null
$script:id = 1

function Receive-Message($ws) {
  $buffer = New-Object byte[] 65536
  $result = New-Object System.Text.StringBuilder
  do {
    $segment = [ArraySegment[byte]]::new($buffer)
    $part = $ws.ReceiveAsync($segment, [Threading.CancellationToken]::None).GetAwaiter().GetResult()
    if ($part.MessageType -eq [Net.WebSockets.WebSocketMessageType]::Close) { throw 'DevTools WebSocket 已关闭' }
    [void]$result.Append([Text.Encoding]::UTF8.GetString($buffer, 0, $part.Count))
  } while (-not $part.EndOfMessage)
  return $result.ToString() | ConvertFrom-Json
}

function Invoke-Cdp($method, $params) {
  $current = $script:id++
  $json = @{ id=$current; method=$method; params=$params } | ConvertTo-Json -Depth 12 -Compress
  $bytes = [Text.Encoding]::UTF8.GetBytes($json)
  $segment = [ArraySegment[byte]]::new($bytes)
  [void]$socket.SendAsync($segment, [Net.WebSockets.WebSocketMessageType]::Text, $true, [Threading.CancellationToken]::None).GetAwaiter().GetResult()
  while ($true) {
    $message = Receive-Message $socket
    if ($message.id -ne $current) { continue }
    if ($message.error) { throw "$method : $($message.error.message)" }
    return $message.result
  }
}

function Eval($expression) {
  $result = Invoke-Cdp 'Runtime.evaluate' @{ expression=$expression; returnByValue=$true; awaitPromise=$true }
  if ($result.exceptionDetails) { throw "页面脚本执行失败：$($result.exceptionDetails.text)" }
  return $result.result.value
}

function Shot($name, $view, $width, $height, $mobile) {
  [void](Invoke-Cdp 'Emulation.setDeviceMetricsOverride' @{ width=$width; height=$height; deviceScaleFactor=1; mobile=$mobile })
  [void](Invoke-Cdp 'Page.navigate' @{ url="${baseUrl}?view=$view" })
  Start-Sleep -Milliseconds 550
  [void](Eval 'Promise.all(Array.from(document.images, img => img.decode().catch(() => null)))')
  $pageHeight = [int](Eval 'Math.ceil(document.documentElement.scrollHeight)')
  $shotHeight = if ($mobile) { [Math]::Min($pageHeight, 2600) } else { [Math]::Max($height, [Math]::Min($pageHeight, 1250)) }
  $result = Invoke-Cdp 'Page.captureScreenshot' @{ format='png'; fromSurface=$true; captureBeyondViewport=$true; clip=@{ x=0; y=0; width=$width; height=$shotHeight; scale=1 } }
  $path = Join-Path $outDir "$name.png"
  [IO.File]::WriteAllBytes($path, [Convert]::FromBase64String($result.data))
  Write-Output "$name $width x $shotHeight"
}

try {
  $target = $null
  for ($i = 0; $i -lt 40; $i++) {
    try {
      $tabs = Invoke-RestMethod "http://127.0.0.1:$port/json"
      $target = $tabs | Where-Object { $_.type -eq 'page' } | Select-Object -First 1
      if ($target) { break }
    } catch { }
    Start-Sleep -Milliseconds 200
  }
  if (-not $target) { throw '未找到 Edge 页面' }
  $socket = [Net.WebSockets.ClientWebSocket]::new()
  [void]$socket.ConnectAsync([Uri]$target.webSocketDebuggerUrl, [Threading.CancellationToken]::None).GetAwaiter().GetResult()
  [void](Invoke-Cdp 'Page.enable' @{})
  $views = @(
    @('01-lobby-desktop','lobby'),
    @('02-create-desktop','create'),
    @('03-waiting-desktop','waiting'),
    @('04-casual-player-desktop','casual'),
    @('05-competitive-player-desktop','competitive'),
    @('06-host-desktop','host'),
    @('07-reveal-request-desktop','reveal'),
    @('08-solo-active-desktop','solo-active'),
    @('09-solo-finished-desktop','solo-finished'),
    @('12-solo-start-desktop','solo-start'),
    @('14-host-restore-desktop','host-restore')
  )
  foreach ($item in $views) { Shot $item[0] $item[1] 1600 1000 $false }
  Shot '10-casual-player-mobile' 'casual' 390 844 $true
  Shot '11-solo-active-mobile' 'solo-active' 390 844 $true
  Shot '13-solo-start-mobile' 'solo-start' 390 844 $true
} finally {
  if ($socket) { $socket.Dispose() }
  if ($process -and -not $process.HasExited) { Stop-Process -Id $process.Id -Force }
}
