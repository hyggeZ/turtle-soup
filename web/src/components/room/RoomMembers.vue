<script setup>
import { computed } from 'vue'

// 回看保留对局身份和答题结果，在线状态只显示在实时房间中。
const props = defineProps({
  room: { type: Object, required: true },
  playerCount: { type: Number, required: true },
  connectedUserId: Number,
})
const showPresence = computed(() => ['waiting', 'active', 'paused'].includes(props.room.status))

function roleName(role) {
  return { host: '主持', player: '玩家', spectator: '旁观' }[role] || '成员'
}

function isOnline(member) {
  // 本页握手完成后立即更新自己的在线状态，其他成员仍由快照更新。
  return member.online || member.id === props.connectedUserId
}
</script>

<template>
  <section class="play-paper play-member-card room-skin room-skin-rust" aria-labelledby="room-members-heading">
    <div class="play-card-heading">
      <h3 id="room-members-heading">房间成员</h3>
      <strong>{{ playerCount }} / {{ room.maxPlayers }} <small>玩家</small></strong>
    </div>
    <div class="play-member-list" role="list">
      <div v-for="member in room.members" :key="member.id" class="play-member" role="listitem">
        <span class="play-avatar" :class="`play-avatar-${member.role}`" :data-portrait="(member.id - 1) % 4 + 1" aria-hidden="true">{{ member.username.slice(0, 1) }}</span>
        <span class="room-member-label room-skin">
          <b :title="member.username">{{ member.username }}</b>
          <small>{{ member.id === room.ownerId ? '房主 · ' : '' }}{{ roleName(member.role) }}{{ member.correct ? ' · 已答对' : '' }}</small>
          <small v-if="showPresence && typeof member.online === 'boolean'" class="room-member-presence" :class="{ 'is-online': isOnline(member) }">
            {{ isOnline(member) ? '在线' : '离线' }}
          </small>
        </span>
      </div>
    </div>
  </section>
</template>

<style scoped>
.room-member-label .room-member-presence.is-online { color: #27584b; }
</style>
