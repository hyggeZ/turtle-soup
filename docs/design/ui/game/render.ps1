param([int]$Port = 9387)

# 把当前 HTML 静态页渲染为 PNG，文字由浏览器排版，不依赖图像模型造字。
$ErrorActionPreference = 'Stop'
[Console]::InputEncoding = [Console]::OutputEncoding = [System.Text.UTF8Encoding]::new($false)
$OutputEncoding = [Console]::OutputEncoding

$dir = $PSScriptRoot
$edge = 'C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe'
$profile = Join-Path $dir '.render-profile'
$args = @('--headless=new', '--disable-gpu', '--hide-scrollbars', '--no-first-run', '--force-device-scale-factor=1', '--remote-allow-origins=*', "--remote-debugging-port=$Port", "--user-data-dir=$profile", '--window-size=1440,1100', 'about:blank')
$proc = Start-Process -FilePath $edge -ArgumentList $args -PassThru -WindowStyle Hidden
$socket = $null
$script:nextId = 1

function Read-Message($ws) {
  $bytes = New-Object byte[] 65536
  $out = New-Object System.Text.StringBuilder
  do {
    $part = $ws.ReceiveAsync([ArraySegment[byte]]::new($bytes), [Threading.CancellationToken]::None).GetAwaiter().GetResult()
    if ($part.MessageType -eq [Net.WebSockets.WebSocketMessageType]::Close) { throw '浏览器已关闭' }
    [void]$out.Append([Text.Encoding]::UTF8.GetString($bytes, 0, $part.Count))
  } while (-not $part.EndOfMessage)
  return $out.ToString() | ConvertFrom-Json
}

function Invoke-Cdp($method, $params) {
  $id = $script:nextId++
  $json = @{ id = $id; method = $method; params = $params } | ConvertTo-Json -Depth 10 -Compress
  $data = [Text.Encoding]::UTF8.GetBytes($json)
  [void]$socket.SendAsync([ArraySegment[byte]]::new($data), [Net.WebSockets.WebSocketMessageType]::Text, $true, [Threading.CancellationToken]::None).GetAwaiter().GetResult()
  while ($true) {
    $answer = Read-Message $socket
    if ($answer.id -ne $id) { continue }
    if ($answer.error) { throw "$method : $($answer.error.message)" }
    return $answer.result
  }
}

try {
  $tab = $null
  for ($i = 0; $i -lt 50; $i++) {
    try {
      $tabs = Invoke-RestMethod "http://127.0.0.1:$Port/json"
      $tab = $tabs | Where-Object { $_.type -eq 'page' } | Select-Object -First 1
      if ($tab) { break }
    } catch { }
    Start-Sleep -Milliseconds 200
  }
  if (-not $tab) { throw '无法连接 Edge DevTools' }
  $socket = [Net.WebSockets.ClientWebSocket]::new()
  [void]$socket.ConnectAsync([Uri]$tab.webSocketDebuggerUrl, [Threading.CancellationToken]::None).GetAwaiter().GetResult()
  [void](Invoke-Cdp 'Page.enable' @{})

  $pages = @(
    @{ name='01-home'; width=1440; height=900 },
    @{ name='01-home-guest'; width=1440; height=900 },
    @{ name='02-login'; width=1440; height=900 },
    @{ name='03-register'; width=1440; height=900 },
    @{ name='04-community'; width=1440; height=1120 },
    @{ name='05-detail'; width=1440; height=1090 },
    @{ name='06-spoiler-confirm'; width=1440; height=900 },
    @{ name='07-editor'; width=1440; height=1040 },
    @{ name='08-profile'; width=1440; height=1040 },
    @{ name='09-profile-edit'; width=1440; height=900 },
    @{ name='01-home'; suffix='-mobile'; width=390; height=1000 },
    @{ name='05-detail'; suffix='-mobile'; width=390; height=1480 }
  )
  foreach ($page in $pages) {
    $width = $page.width
    $height = $page.height
    $name = $page.name
    $suffix = if ($page.suffix) { $page.suffix } else { '' }
    $source = Join-Path $dir "$name.html"
    $url = [System.Uri]::new($source).AbsoluteUri
    [void](Invoke-Cdp 'Emulation.setDeviceMetricsOverride' @{ width=$width; height=$height; deviceScaleFactor=1; mobile=($width -lt 500) })
    [void](Invoke-Cdp 'Page.navigate' @{ url=$url })
    Start-Sleep -Milliseconds 500
    $shot = Invoke-Cdp 'Page.captureScreenshot' @{ format='png'; fromSurface=$true; captureBeyondViewport=$true; clip=@{ x=0; y=0; width=$width; height=$height; scale=1 } }
    $target = Join-Path $dir "$name$suffix.png"
    [IO.File]::WriteAllBytes($target, [Convert]::FromBase64String($shot.data))
    Write-Output $target
  }
} finally {
  if ($socket) { $socket.Dispose() }
  if ($proc -and -not $proc.HasExited) { Stop-Process -Id $proc.Id -Force }
  # 仅清理本脚本在静态图目录创建的浏览器配置。
  $safeRoot = [IO.Path]::GetFullPath($dir)
  $safeProfile = [IO.Path]::GetFullPath($profile)
  if (-not $safeProfile.StartsWith($safeRoot + [IO.Path]::DirectorySeparatorChar, [StringComparison]::OrdinalIgnoreCase)) {
    throw '浏览器配置目录超出静态图目录，拒绝清理'
  }
  if (Test-Path -LiteralPath $safeProfile) {
    for ($i = 0; $i -lt 8; $i++) {
      try { Remove-Item -LiteralPath $safeProfile -Recurse -Force -ErrorAction Stop; break }
      catch { Start-Sleep -Milliseconds 250 }
    }
    if (Test-Path -LiteralPath $safeProfile) { Write-Warning '浏览器配置仍被占用，请稍后手动清理 .render-profile' }
  }
}
