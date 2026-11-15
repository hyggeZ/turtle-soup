<script setup>
// 游戏首页只负责展示三个真实入口，房间和题目的状态由各自页面管理。
const emit = defineEmits(['lobby', 'solo', 'community'])
</script>

<template>
  <section class="home-hero" aria-labelledby="home-title">
    <div class="home-hero-inner">
      <p class="home-eyebrow">TURTLE SOUP · 推理游戏</p>
      <h1 id="home-title">今晚，来猜<span class="home-break">一个故事。</span></h1>
      <span class="home-title-mark" aria-hidden="true"></span>

      <div class="home-routes" aria-label="选择玩法">
        <button class="home-route home-route--main" @click="emit('lobby')">
          <span class="home-route-kicker">01 / 和朋友一起玩</span>
          <strong>多人房间</strong>
          <span>创建房间，或凭房间号加入一局</span>
          <span class="home-route-arrow" aria-hidden="true">↗</span>
        </button>
        <button class="home-route home-route--solo" @click="emit('solo')">
          <span class="home-route-kicker">02 / 自己推理</span>
          <strong>单人模式 <span aria-hidden="true">↗</span></strong>
          <span>选一道题，向 AI 主持人提问</span>
        </button>
        <button class="home-route home-route--stories" @click="emit('community')">
          <span class="home-route-kicker">03 / 发现故事</span>
          <strong>题目社区 <span aria-hidden="true">↗</span></strong>
          <span>读汤面、试玩、发布你的题目</span>
        </button>
      </div>
    </div>
  </section>
</template>
