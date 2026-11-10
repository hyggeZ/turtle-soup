<script setup>
// 详情页默认展示汤面；作者可直接查看汤底，其他用户需要主动申请。
import { computed, onMounted, ref, watch } from 'vue'
import { request, send } from '../api'
const props = defineProps({ id: Number, user: Object })
const emit = defineEmits(['back', 'profile', 'play', 'solo', 'edit', 'deleted'])
const soup = ref(null)
const isAuthor = computed(() => props.user && soup.value?.authorId === props.user.id)
const comments = ref([])
const answer = ref('')
const comment = ref('')
const spoiler = ref(false)
const error = ref('')
const deleting = ref(false)
async function load() {
  soup.value = null
  comments.value = []
  answer.value = ''
  try {
    soup.value = await request(`/api/soups/${props.id}`)
    comments.value = await request(`/api/soups/${props.id}/comments`)
    answer.value = ''; spoiler.value = false; error.value = ''
  } catch (cause) { error.value = cause.message }
}
async function toggle(kind, active) {
  try {
    await send(`/api/soups/${props.id}/${kind}`, active ? 'DELETE' : 'PUT')
    soup.value = await request(`/api/soups/${props.id}`)
  } catch (cause) { error.value = cause.message }
}
async function showAnswer() {
  if (isAuthor.value && typeof soup.value.answer === 'string') {
    answer.value = soup.value.answer
    return
  }
  if (!window.confirm('查看汤底会产生剧透，之后不能以玩家身份参与这道题。确定查看吗？')) return
  try { answer.value = (await send(`/api/soups/${props.id}/answer`, 'POST')).answer }
  catch (cause) { error.value = cause.message }
}
async function showComment(item) {
  if (!window.confirm('展开剧透评论后，不能再以玩家身份参与这道题。确定展开吗？')) return
  try {
    const result = await send(`/api/soups/${props.id}/comments/${item.id}/spoiler`, 'POST')
    item.body = result.body
    error.value = ''
  } catch (cause) { error.value = cause.message }
}
async function addComment() {
  if (!comment.value.trim()) return
  try {
    await send(`/api/soups/${props.id}/comments`, 'POST', { body: comment.value, spoiler: spoiler.value })
    comment.value = ''
    spoiler.value = false
    comments.value = await request(`/api/soups/${props.id}/comments`)
    soup.value = await request(`/api/soups/${props.id}`)
  } catch (cause) { error.value = cause.message }
}
// 删除由服务端校验作者身份，并保留已有对局的题目快照。
async function deleteSoup() {
  if (deleting.value || !window.confirm('删除后题目不再公开，确定删除吗？')) return
  deleting.value = true
  error.value = ''
  try {
    await send(`/api/soups/${props.id}`, 'DELETE')
    emit('deleted')
  } catch (cause) { error.value = cause.message }
  finally { deleting.value = false }
}
watch(() => props.id, load)
onMounted(load)
</script>
<template>
  <section class="panel" v-if="soup">
    <button class="text-button" @click="emit('back')">← 返回广场</button><h2>{{ soup.title }}</h2>
    <p class="meta">{{ soup.genre }} · 难度 {{ soup.difficulty }}/5 · 恐怖 {{ soup.horror }}/5 · <button class="text-button" @click="emit('profile', soup.authorId)">@{{ soup.author }}</button></p>
    <p class="surface full">{{ soup.surface }}</p>
    <div class="actions"><button v-if="user" @click="toggle('like', soup.liked)">{{ soup.liked ? '♥ 已点赞' : '♡ 点赞' }} {{ soup.likes }}</button>
      <button v-if="user" @click="toggle('bookmark', soup.bookmarked)">{{ soup.bookmarked ? '★ 已收藏' : '☆ 收藏' }} {{ soup.bookmarks }}</button>
      <button v-if="user && !answer" @click="showAnswer">查看汤底</button>
      <button v-if="user" @click="emit('play', soup.id)">用此题建房</button>
      <button @click="emit('solo', soup.id)">单人试玩</button></div>
    <div v-if="isAuthor" class="author-actions">
      <button class="small-button" :disabled="deleting" @click="emit('edit', soup.id)">编辑题目</button>
      <button class="small-button" :disabled="deleting" @click="deleteSoup">{{ deleting ? '正在删除…' : '删除题目' }}</button>
    </div>
    <p v-if="!user" class="muted">登录后可互动或查看汤底。</p>
    <div v-if="answer" class="answer"><h3>汤底</h3><p>{{ answer }}</p></div>
    <h3>评论 {{ soup.comments }}</h3>
    <form v-if="user" @submit.prevent="addComment">
      <textarea v-model="comment" rows="3" maxlength="500" placeholder="写下你的想法" required></textarea>
      <label class="check-line"><input v-model="spoiler" type="checkbox" />这条评论包含剧透</label>
      <button>发表评论</button>
    </form>
    <p v-if="!comments.length" class="muted">还没有评论。</p>
    <div v-for="item in comments" :key="item.id" class="comment">
      <button class="text-button" @click="emit('profile', item.authorId)">@{{ item.author }}</button>
      <p v-if="item.spoiler && !item.body" class="muted">
        剧透评论
        <button v-if="user" class="text-button" @click="showComment(item)">展开</button>
        <span v-else>· 登录后可查看</span>
      </p>
      <p v-else>{{ item.body }}</p>
    </div>
    <p v-if="error" role="alert">{{ error }}</p>
  </section><p v-else-if="error" role="alert">{{ error }}</p>
</template>
