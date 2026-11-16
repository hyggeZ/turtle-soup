// 玩法页面静态稿：仅用于核对布局、文字与交互状态，不连接业务接口。
const view = new URLSearchParams(location.search).get('view') || 'lobby'
const app = document.querySelector('#app')
const surface = '深夜，四人离开包厢后，侦探在里面发现一名男子倒在地上。门从里面反锁，桌上只有一只打翻的杯子和一块停走的怀表。警方确认他并非中毒，也不是被刀枪所伤。发生了什么？'
const answer = '男子独处时突发心脏病，倒地时撞翻了杯子，怀表也在跌落时摔坏。门是他为躲雨自行反锁的。没有凶手。'

const button = (label, tone = 'coral', extra = '') => `<button class="ticket-button ticket-${tone} ${extra}" type="button"><span>${label}</span></button>`
const avatar = (name, tone = 'coral', extra = '') => `<span class="avatar avatar-${tone} ${extra}" aria-label="${name}">${name.slice(0, 1)}</span>`
const pill = (label, tone = '') => `<span class="stamp ${tone}">${label}</span>`

function heading(title, back, details = '') {
  return `<header class="page-heading">
    <button class="back-ticket" type="button"><span>←</span> ${back}</button>
    <div class="page-title"><span class="title-loop" aria-hidden="true"></span><h1>${title}</h1><i></i></div>
    <div class="heading-details">${details}</div>
  </header>`
}

function shell(title, back, body, details = '', cls = '') {
  return `<main class="scene ${cls}"><div class="scene-content">${heading(title, back, details)}${body}</div></main>`
}

function caseCard(compact = false) {
  return `<section class="paper case-card ${compact ? 'case-compact' : ''}">
    <div class="paper-header"><span class="paper-tab">◈ 汤面 · 01</span><span class="paper-ref">CASE FILE / 014</span></div>
    <div class="case-inner"><figure class="evidence-photo"><img src="assets/evidence-cup-watch.png" alt="打翻的杯子和怀表" /><figcaption>现场记录</figcaption></figure>
      <div class="case-copy"><h2>雨停之前</h2><div class="case-tags">${pill('本格')}${pill('难度 3 / 5')}${pill('恐怖 1 / 5')}</div><p>${surface}</p></div></div>
    <div class="paper-foot"><span>推理之前，请先阅读汤面</span><span class="fingerprint" aria-hidden="true">◎</span></div>
  </section>`
}

function members(waiting = false) {
  const list = [
    ['侦探甲', '房主 · 主持', 'coral'],
    ['侦探乙', '玩家', 'cyan'],
    ['侦探丙', '玩家', 'gold'],
    ['侦探丁', waiting ? '申请主持' : '玩家', 'purple'],
  ]
  return `<section class="paper member-card"><div class="mini-head"><div><span class="eyebrow">PLAYERS</span><h3>房间成员</h3></div><strong>3 / 6 <small>玩家</small></strong></div>
    <div class="member-list">${list.map(([name, role, tone]) => `<div class="member-row">${avatar(name, tone)}<span><b>${name}</b><small>${role}</small></span><i class="member-status"></i></div>`).join('')}</div>
  </section>`
}

function roomSummary(mode = '共探模式', status = '进行中') {
  return `<div class="room-ribbon"><div class="ribbon-part"><small>房间码</small><strong>7F3K</strong><button class="copy-button" type="button">复制</button></div><div class="ribbon-divider"></div><div class="ribbon-part"><small>玩法</small><strong>${mode}</strong></div><div class="ribbon-divider"></div><div class="ribbon-part"><small>状态</small><strong>${status}</strong></div></div>`
}

function record() {
  const rows = [
    ['侦探乙', '死者是被他人杀害的吗？', '否', 'no', 'cyan'],
    ['侦探丙', '杯中装的是毒药吗？', '否', 'no', 'gold'],
    ['侦探丁', '怀表和死因有关吗？', '无关', 'maybe', 'purple'],
    ['侦探乙', '门是死者自己锁上的吗？', '是', 'yes', 'cyan'],
  ]
  return `<section class="paper record-card"><div class="mini-head"><div><span class="eyebrow">QUESTION LOG</span><h3>提问记录</h3></div><span class="record-count">04 条线索</span></div>
    <div class="record-list">${rows.map(([name, question, result, state, tone], i) => `<div class="record-row"><span class="record-index">${String(i + 1).padStart(2, '0')}</span>${avatar(name, tone)}<div class="record-copy"><small>${name}</small><p>${question}</p></div><span class="verdict verdict-${state}">${result}</span></div>`).join('')}</div>
  </section>`
}

function actionCard(type) {
  if (type === 'ask') return `<section class="paper action-card action-ask"><div class="action-title"><span class="action-icon">?</span><div><small>QUESTION</small><h3>提出问题</h3></div></div><label class="input-paper"><span>写下你想向主持人确认的事</span><textarea rows="2" placeholder="例如：死者是被他人杀害的吗？"></textarea></label><div class="action-foot"><span>主持人只会回答 是 / 否 / 无关</span>${button('提问　→', 'cyan')}</div></section>`
  return `<section class="paper action-card action-restore"><div class="action-title"><span class="action-icon">⌕</span><div><small>RESTORE</small><h3>还原真相</h3></div></div><label class="input-paper"><span>说出你推断的完整经过</span><textarea rows="2" placeholder="我认为事情的真相是……"></textarea></label><div class="action-foot"><span>共探模式下，你的还原内容不会公开</span>${button('提交还原　→', 'coral')}</div></section>`
}

function lobby() {
  const rooms = [
    ['雨停之前', '共探', '本格', '3 / 6', '侦探甲', 'coral'],
    ['最后一班电梯', '竞答', '变格', '2 / 4', '月下', 'cyan'],
    ['没有寄出的信', '共探', '本格', '4 / 5', '星野', 'gold'],
  ]
  return shell('房间大厅', '返回首页', `<div class="lobby-grid"><div class="lobby-main">
      <div class="board-heading"><div><span class="eyebrow">OPEN ROOMS</span><h2>正在招募的房间</h2></div><span class="board-count">03 间可加入</span></div>
      <div class="room-list">${rooms.map(([name, mode, genre, count, host, tone], i) => `<article class="room-list-row"><span class="row-number">0${i + 1}</span><div class="room-list-body"><h3>${name}</h3><div class="room-list-tags">${pill(mode, tone)}${pill(genre)}<span>主持：${host}</span><span>玩家 ${count}</span></div></div>${button('进入房间　→', i === 1 ? 'cyan' : 'coral', 'ticket-small')}</article>`).join('')}</div>
      <div class="lobby-note"><span>✧</span> 竞答模式开始后不可加入；共探模式进行中仍可入场。</div>
    </div><aside class="lobby-tools"><section class="paper join-card"><span class="eyebrow">PRIVATE ROOM</span><h2>输入房间码</h2><label class="room-code-input"><span>房间码</span><input value="7F3K" maxlength="32" /></label>${button('加入房间　→', 'cyan')}</section>
      <section class="create-card"><div class="create-symbol">＋</div><span class="eyebrow">NEW ROOM</span><h2>创建自己的房间</h2><p>选一道汤题，决定玩法与人数。</p>${button('创建房间　→', 'coral')}</section></aside></div>`, '', 'lobby-scene')
}

function create() {
  return lobby().replace('</main>', `<div class="modal-layer"><section class="paper create-dialog"><div class="dialog-head"><div><span class="eyebrow">NEW ROOM</span><h2>创建房间</h2></div><button class="dialog-close" type="button">×</button></div>
      <div class="dialog-content"><div class="dialog-left"><h3>① 选择汤题</h3><div class="soup-choice selected"><span class="choice-check">✓</span><div><b>雨停之前</b><small>本格 · 难度 3 · 恐怖 1</small></div><span>查看汤面 ↗</span></div><div class="soup-choice"><span class="choice-check"></span><div><b>午夜门铃</b><small>变格 · 难度 4 · 恐怖 2</small></div></div><div class="soup-choice"><span class="choice-check"></span><div><b>消失的车票</b><small>本格 · 难度 2 · 恐怖 0</small></div></div><button class="more-soup" type="button">打开题目列表　→</button></div>
      <div class="dialog-right"><h3>② 房间设置</h3><label class="form-label">玩法模式<div class="option-pair"><button class="option selected" type="button"><b>共探</b><small>自由提问</small></button><button class="option" type="button"><b>竞答</b><small>轮流提问</small></button></div></label><label class="form-label">人数上限<select><option>6 人</option></select></label><label class="form-label">主持方式<div class="option-pair"><button class="option selected" type="button"><b>自己主持</b></button><button class="option" type="button"><b>招募主持</b></button></div></label><label class="check-line"><span class="check-box">✓</span>将房间发布到大厅</label></div></div>
      <div class="dialog-foot"><span>创建后可以在开局前修改设置</span>${button('创建并进入　→', 'coral')}</div></section></div></main>`)
}

function waiting() {
  return shell('共探房间', '返回大厅', `${roomSummary('共探模式', '等待开始')}<div class="room-grid room-grid-waiting"><div>${caseCard()}</div><div class="room-side">${members(true)}<section class="paper waiting-card"><div class="mini-head"><div><span class="eyebrow">READY TO PLAY</span><h3>等待开局</h3></div>${pill('主持申请 1', 'stamp-red')}</div><div class="request-row">${avatar('侦探丁', 'purple')}<span><b>侦探丁申请主持</b><small>房主确认后即可主持本局</small></span>${button('同意', 'cyan', 'ticket-small')}</div><div class="waiting-actions">${button('修改设置', 'plain')}${button('关闭房间', 'plain')}${button('开始游戏　→', 'coral')}</div></section></div></div>`, '', 'room-scene')
}

function casual() {
  return shell('共探房间', '返回大厅', `${roomSummary()}<div class="room-grid"><div>${caseCard(true)}${members()}</div><div>${record()}</div></div><div class="action-grid">${actionCard('ask')}${actionCard('restore')}</div><div class="answer-request">${button('申请看答案', 'plain')}<span>主持人同意后，你会退出玩家席位并可以旁观。</span></div>`, '<span class="head-ticket cyan">● 实时连接正常</span>', 'room-scene')
}

function competitive() {
  return shell('竞答房间', '返回大厅', `<div class="turn-banner"><span class="turn-clock">00:38</span><div><small>当前回合</small><strong>轮到你提问</strong></div><span class="turn-order">第 03 / 06 轮</span></div>${roomSummary('竞答模式', '进行中')}<div class="room-grid competitive-grid"><div>${caseCard(true)}${members()}</div><div>${record()}</div></div><div class="competition-action"><div class="action-title"><span class="action-icon">?</span><div><small>YOUR TURN</small><h3>在倒计时结束前提问</h3></div></div><label class="input-paper"><textarea rows="2" placeholder="输入一个可以回答是、否或无关的问题……"></textarea></label>${button('提交问题　→', 'cyan')}<span>其他玩家等待本回合结束后继续。</span></div>`, '<span class="head-ticket">房间码 <b>8K2M</b></span>', 'room-scene competitive-scene')
}

function host() {
  return shell('主持人席位', '返回大厅', `${roomSummary()}<div class="host-grid"><div class="host-left">${caseCard(true)}<section class="paper host-answer"><div class="mini-head"><div><span class="eyebrow">PRIVATE / HOST ONLY</span><h3>汤底</h3></div>${pill('仅主持人可见', 'stamp-red')}</div><p>${answer}</p></section></div><div class="host-right"><section class="paper queue-card"><div class="mini-head"><div><span class="eyebrow">PENDING QUESTION</span><h3>待判定的问题</h3></div>${pill('01', 'stamp-red')}</div><div class="pending-body">${avatar('侦探乙', 'cyan')}<div><small>侦探乙 提问</small><p>死者是被他人杀害的吗？</p></div></div><div class="verdict-actions">${button('是', 'cyan')}${button('否', 'coral')}${button('无关', 'gold')}</div></section>
      <section class="paper queue-card reveal-queue"><div class="mini-head"><div><span class="eyebrow">ANSWER REQUEST</span><h3>查看汤底申请</h3></div>${pill('01', 'stamp-red')}</div><div class="pending-body">${avatar('侦探丙', 'gold')}<div><small>侦探丙 申请看答案</small><p>同意后，这位玩家会退出玩家席位。</p></div></div><div class="approval-actions">${button('拒绝', 'plain')}${button('同意查看', 'cyan')}</div></section></div></div>`, '', 'room-scene host-scene')
}

function hostRestore() {
  return host()
    .replace('PENDING QUESTION', 'PRIVATE RESTORE')
    .replace('待判定的问题', '待判定的还原')
    .replace('侦探乙 提问', '侦探乙 提交还原')
    .replace('死者是被他人杀害的吗？', '我认为死者摔倒撞翻了杯子，但不确定为什么倒下。')
    .replace(`<div class="verdict-actions">${button('是', 'cyan')}${button('否', 'coral')}${button('无关', 'gold')}</div>`, `<div class="verdict-actions">${button('正确', 'cyan')}${button('错误', 'coral')}${button('不完整', 'gold')}</div>`)
}

function reveal() {
  return casual().replace('</main>', `<div class="modal-layer"><section class="paper confirm-dialog"><span class="dialog-symbol">◉</span><span class="eyebrow">ANSWER REQUEST</span><h2>申请查看汤底？</h2><p>主持人同意后，你会立即退出玩家席位，本局只能旁观。已经看过汤底的账号不能重新以玩家身份加入同一道题的对局。</p><div class="dialog-actions">${button('暂不查看', 'plain')}${button('确认申请　→', 'coral')}</div></section></div></main>`)
}

function soloStart() {
  const soups = [
    ['雨停之前', '本格', '3', '1', true],
    ['午夜门铃', '变格', '4', '2', false],
    ['消失的车票', '本格', '2', '0', false],
  ]
  return shell('单人模式', '返回首页', `<div class="solo-ribbon"><div class="solo-ai-seal">AI</div><div><strong>AI 主持</strong><span>只回答 是 / 否 / 无关</span></div><span class="solo-state">选择汤题</span></div>
    <div class="solo-start-grid"><section class="paper solo-picker"><div class="mini-head"><div><span class="eyebrow">CHOOSE A CASE</span><h3>选择汤题</h3></div><span class="record-count">共 03 题</span></div>
      <div class="solo-pick-list">${soups.map(([name, genre, difficulty, horror, selected], i) => `<button class="solo-pick ${selected ? 'selected' : ''}" type="button"><span class="pick-number">0${i + 1}</span><span class="pick-copy"><b>${name}</b><small>${genre} · 难度 ${difficulty} / 5 · 恐怖 ${horror} / 5</small></span><span class="pick-mark">${selected ? '✓' : '→'}</span></button>`).join('')}</div>
      <div class="solo-pick-note">已看过汤底的题目不会出现在可玩列表中。</div></section>
      <div class="solo-selected">${caseCard()}<div class="solo-start-actions"><span><b>雨停之前</b> 已选中</span>${button('开始推理　→', 'cyan')}</div></div></div>`, '', 'solo-scene solo-start-scene')
}

function solo(finished = false) {
  const events = finished ? [
    ['我认为没有凶手，死者是因突发疾病倒地。', '不完整', 'maybe'],
    ['男子突发心脏病，摔坏怀表、撞翻杯子；门是他自己锁的。', '正确', 'yes'],
  ] : [
    ['死者是被别人杀害的吗？', '否', 'no'],
    ['杯中装着毒药吗？', '否', 'no'],
    ['门是死者自己锁上的？', '是', 'yes'],
  ]
  return shell('单人模式', '返回首页', `<div class="solo-ribbon"><div class="solo-ai-seal">AI</div><div><strong>AI 主持</strong><span>只回答 是 / 否 / 无关</span></div><span class="solo-state">${finished ? '本局已完成' : '推理进行中'}</span></div><div class="solo-grid"><div>${caseCard()}${finished ? `<section class="paper solution-card"><div class="mini-head"><div><span class="eyebrow">CASE CLOSED</span><h3>汤底</h3></div>${pill('还原成功', 'stamp-cyan')}</div><p>${answer}</p></section>` : ''}</div><div class="solo-side"><section class="paper solo-log"><div class="mini-head"><div><span class="eyebrow">DEDUCTION LOG</span><h3>推理记录</h3></div><span class="record-count">${events.length} 条</span></div><div class="solo-log-list">${events.map(([question, result, tone], i) => `<div class="solo-log-row"><span class="record-index">${String(i + 1).padStart(2, '0')}</span><p>${question}</p><span class="verdict verdict-${tone}">${result}</span></div>`).join('')}</div></section>${finished ? `<section class="paper finished-card"><span class="finish-symbol">✓</span><h3>真相已还原</h3><p>你找到了杯子、怀表和反锁的门之间的联系。</p>${button('再选一道题　→', 'cyan')}</section>` : `<section class="paper solo-control"><div class="action-title"><span class="action-icon">?</span><div><small>ASK AI HOST</small><h3>继续提问</h3></div></div><label class="input-paper"><textarea rows="2" placeholder="输入一个可以回答是、否或无关的问题……"></textarea></label><div class="solo-buttons">${button('提问　→', 'cyan')}${button('提交还原', 'coral')}</div><button class="answer-text-button" type="button">申请看答案</button></section>`}</div></div>`, '', 'solo-scene')
}

const pages = { lobby, create, waiting, casual, competitive, host, 'host-restore': hostRestore, reveal, 'solo-start': soloStart, 'solo-active': () => solo(false), 'solo-finished': () => solo(true) }
app.innerHTML = (pages[view] || lobby)()
