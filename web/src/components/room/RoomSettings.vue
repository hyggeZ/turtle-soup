<script setup>
// 等待开局时，房主可以调整题目和房间规则。
import { reactive, ref, watch } from 'vue'
import { send } from '../../api'
import SoupPicker from '../shared/SoupPicker.vue'

const props = defineProps({ room: { type: Object, required: true } })
const emit = defineEmits(['saved', 'close'])
const form = reactive({ soupId: 0, mode: 'casual', maxPlayers: 6, public: false })
const saving = ref(false)
const error = ref('')

watch(
  () => [props.room.id, props.room.soupId, props.room.mode, props.room.maxPlayers, props.room.public],
  (values, previous) => {
    // 定期快照会替换 room 对象；设置没变时保留正在编辑的输入。
    if (previous && values.every((value, index) => value === previous[index])) return
    form.soupId = props.room.soupId
    form.mode = props.room.mode
    form.maxPlayers = props.room.maxPlayers
    form.public = props.room.public
  },
  { immediate: true },
)

async function save() {
  if (!form.soupId || saving.value) return
  saving.value = true
  try {
    await send(`/api/rooms/${props.room.id}/settings`, 'PATCH', { ...form })
    error.value = ''
    emit('saved')
  } catch (cause) {
    error.value = cause.message
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <section class="play-room-settings">
    <div class="play-card-heading"><div><span class="play-eyebrow">ROOM SETTINGS</span><h3>修改设置</h3></div><button type="button" class="play-link" @click="emit('close')">收起</button></div>
    <form @submit.prevent="save">
      <SoupPicker v-model="form.soupId" />
      <div class="play-settings-row">
        <label class="play-field">玩法
          <select v-model="form.mode"><option value="casual">共探</option><option value="competitive">竞答</option></select>
        </label>
        <label class="play-field">玩家人数上限
          <input v-model.number="form.maxPlayers" type="number" :min="Math.max(2, room.members.filter(item => item.role === 'player').length)" max="20" required />
        </label>
      </div>
      <label class="play-check"><input v-model="form.public" type="checkbox" />在大厅公开</label>
      <p v-if="error" class="play-alert" role="alert">{{ error }}</p>
      <button class="play-ticket play-ticket-small play-ticket-coral" :disabled="saving || !form.soupId">{{ saving ? '保存中…' : '保存设置' }}</button>
    </form>
  </section>
</template>
