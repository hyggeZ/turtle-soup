<script setup>
// 首页集中提供各功能入口，账号信息只决定显示“登录”还是“我的主页”。
defineProps({ user: { type: Object, default: null } })
const emit = defineEmits(['lobby', 'solo', 'community', 'login', 'profile', 'write'])

// 入口插画画进票面，标题和操作仍由页面渲染。
const routes = [
  { key: 'room', event: 'lobby', title: '多人房间', detail: '和朋友一起推理' },
  { key: 'solo', event: 'solo', title: '单人模式', detail: '独自推理，解开谜题' },
  { key: 'community', event: 'community', title: '题目社区', detail: '阅读、试玩、发布题目' },
]
</script>

<template>
  <main class="game-ui game-home">
    <div class="game-home-inner">
      <div class="game-brand">
        <span class="game-turtle-mark" aria-hidden="true"></span>
        <div><h1>海龟汤</h1><i aria-hidden="true"></i></div>
      </div>
      <div class="game-home-account">
        <button v-if="user" class="game-home-link" @click="emit('profile', user.id)">我的主页 <span aria-hidden="true">›</span></button>
        <button v-else class="game-home-link" @click="emit('login')">登录 / 注册 <span aria-hidden="true">›</span></button>
      </div>
      <div class="game-route-stack" aria-label="选择玩法">
        <button
          v-for="route in routes"
          :key="route.key"
          class="game-route"
          :class="`game-route--${route.key}`"
          @click="emit(route.event)"
        >
          <span class="game-route-copy">
            <strong class="game-route-title">{{ route.title }}</strong>
            <span class="game-route-detail">{{ route.detail }}</span>
            <span class="game-route-enter" aria-hidden="true">进入 <b>→</b></span>
          </span>
        </button>
      </div>
      <div class="game-home-actions">
        <button class="game-home-link game-home-write" @click="emit('write')"><span aria-hidden="true">✎</span> 发布题目 <span aria-hidden="true">›</span></button>
        <a v-if="user?.isAdmin" class="game-home-link game-home-admin" href="/admin"><span aria-hidden="true">⚙</span> 管理后台</a>
      </div>
    </div>
  </main>
</template>
