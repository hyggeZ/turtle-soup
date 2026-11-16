<script setup>
// 首页集中提供各功能入口，账号信息只决定显示“登录”还是“我的主页”。
defineProps({ user: { type: Object, default: null } })
const emit = defineEmits(['lobby', 'solo', 'community', 'login', 'profile', 'write'])
</script>

<template>
  <main class="game-ui game-home">
    <div class="game-home-inner">
      <div class="game-brand">
        <span class="game-turtle-mark" aria-hidden="true"></span>
        <div><strong>海龟汤</strong><i aria-hidden="true"></i></div>
      </div>
      <div class="game-home-account" :class="{ 'game-home-account--admin': user?.isAdmin }">
        <button v-if="user" class="game-ticket" @click="emit('profile', user.id)">◎ 我的主页 <span aria-hidden="true">›</span></button>
        <button v-else class="game-ticket" @click="emit('login')">◎ 登录 / 注册 <span aria-hidden="true">›</span></button>
        <a v-if="user?.isAdmin" class="game-ticket game-admin-ticket" href="/admin">⚙ 管理后台 <span aria-hidden="true">›</span></a>
      </div>
      <div class="game-home-content">
        <h1>今晚，来猜一个故事。</h1>
        <div class="game-route-stack" aria-label="选择玩法">
          <button class="game-route game-route--room" @click="emit('lobby')">
            <span class="game-route-symbol" aria-hidden="true">众</span>
            <span class="game-route-copy"><small>01 / 与朋友一起推理</small><strong>多人房间</strong><span>建房、加入，或在大厅找一局</span></span>
            <span class="game-route-enter">进入 ↗</span>
          </button>
          <button class="game-route game-route--solo" @click="emit('solo')">
            <span class="game-route-symbol" aria-hidden="true">谜</span>
            <span class="game-route-copy"><small>02 / 自己推理</small><strong>单人模式</strong><span>向 AI 主持人提问</span></span>
            <span class="game-route-enter">进入 ↗</span>
          </button>
          <button class="game-route game-route--community" @click="emit('community')">
            <span class="game-route-symbol" aria-hidden="true">录</span>
            <span class="game-route-copy"><small>03 / 发现更多故事</small><strong>题目社区</strong><span>阅读、收藏和发布汤题</span></span>
            <span class="game-route-enter">进入 ↗</span>
          </button>
        </div>
        <button class="game-ticket game-home-write" @click="emit('write')">✎ 发布题目 <span aria-hidden="true">↗</span></button>
      </div>
    </div>
  </main>
</template>
