<script setup>
// 房间成员只展示服务端快照中的当前身份与答题状态。
defineProps({ room: { type: Object, required: true }, playerCount: { type: Number, required: true } })

function roleName(role) {
  return { host: '主持', player: '玩家', spectator: '旁观' }[role] || '成员'
}
</script>

<template>
  <section class="play-paper play-member-card room-skin room-skin-rust">
    <div class="play-card-heading">
      <h3>房间成员</h3>
      <strong>{{ playerCount }} / {{ room.maxPlayers }} <small>玩家</small></strong>
    </div>
    <div class="play-member-list">
      <div v-for="member in room.members" :key="member.id" class="play-member">
        <span class="play-avatar" :class="`play-avatar-${member.role}`" :data-portrait="(member.id - 1) % 4 + 1" aria-hidden="true">{{ member.username.slice(0, 1) }}</span>
        <span class="room-member-label room-skin">
          <b :title="member.username">{{ member.username }}</b>
          <small>{{ member.id === room.ownerId ? '房主 · ' : '' }}{{ roleName(member.role) }}{{ member.correct ? ' · 已答对' : '' }}</small>
        </span>
      </div>
    </div>
  </section>
</template>
