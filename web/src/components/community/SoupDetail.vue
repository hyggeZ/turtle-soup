<script setup>
// 详情页默认展示汤面；作者可直接查看汤底，其他用户需要主动申请。
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { request, send } from '../../api'
import evidenceImage from '../../assets/evidence-collage.webp'
import ConfirmDialog from '../shared/ConfirmDialog.vue'
const props = defineProps({ id: Number, user: Object })
const emit = defineEmits(['back', 'profile', 'edit', 'deleted'])
const soup = ref(null)
const isAuthor = computed(() => props.user && soup.value?.authorId === props.user.id)
const comments = ref([])
const commentsBusy = ref(false)
const commentsError = ref('')
const hasMoreComments = ref(false)
const answer = ref('')
const comment = ref('')
const spoiler = ref(false)
const error = ref('')
const deleting = ref(false)
const loading = ref(false)
const posting = ref(false)
const confirmOpen = ref(false)
const confirmType = ref('')
const confirmItem = ref(null)
const confirmTitle = computed(() => ({ answer: '确定查看汤底？', comment: '确定查看评论？', delete: '确定删除题目？' })[confirmType.value] || '')
const confirmMessage = computed(() => ({
  answer: '查看后会知道完整真相，之后不能再以玩家身份参与这道题的对局。',
  comment: '评论可能包含汤底。查看后不能再以玩家身份参与这道题的对局。',
  delete: '删除后题目不再公开，已有对局记录仍会保留。',
})[confirmType.value] || '')
const confirmText = computed(() => ({ answer: '确认查看', comment: '确认查看', delete: '删除题目' })[confirmType.value] || '确定')
let loadVersion = 0
let commentsVersion = 0

function isCurrent(id, version) {
  return props.id === id && loadVersion === version
}

// 评论按 ID 倒序分页，游标始终取当前列表的最后一条。
async function loadComments(id, version, before = 0) {
  const current = before ? commentsVersion : ++commentsVersion
  const path = `/api/soups/${id}/comments${before ? `?before=${before}` : ''}`
  commentsBusy.value = true
  commentsError.value = ''
  if (!before) {
    comments.value = []
    hasMoreComments.value = false
  }
  try {
    const next = await request(path)
    if (!isCurrent(id, version) || current !== commentsVersion) return
    // 列表响应只用于展示评论信息；正文必须由主动展开操作取得。
    const folded = next.map(item => ({ ...item, body: '' }))
    comments.value = before ? [...comments.value, ...folded] : folded
    hasMoreComments.value = next.length === 100
  } catch (cause) {
    if (isCurrent(id, version) && current === commentsVersion) commentsError.value = cause.message
  } finally {
    if (isCurrent(id, version) && current === commentsVersion) commentsBusy.value = false
  }
}

function loadMoreComments() {
  if (commentsBusy.value || !hasMoreComments.value || !comments.value.length) return
  loadComments(props.id, loadVersion, comments.value.at(-1).id)
}

async function load() {
  const id = props.id
  const version = ++loadVersion
  commentsVersion++
  soup.value = null
  comments.value = []
  commentsBusy.value = false
  commentsError.value = ''
  hasMoreComments.value = false
  answer.value = ''
  comment.value = ''
  spoiler.value = false
  error.value = ''
  confirmOpen.value = false
  confirmItem.value = null
  loading.value = true
  try {
    const nextSoup = await request(`/api/soups/${id}`)
    if (!isCurrent(id, version)) return
    soup.value = nextSoup
    if (!nextSoup.hidden) await loadComments(id, version)
  } catch (cause) {
    if (isCurrent(id, version)) error.value = cause.message
  } finally {
    if (isCurrent(id, version)) loading.value = false
  }
}
async function toggle(kind, active) {
  const id = props.id
  const version = loadVersion
  try {
    await send(`/api/soups/${id}/${kind}`, active ? 'DELETE' : 'PUT')
    const next = await request(`/api/soups/${id}`)
    if (isCurrent(id, version)) soup.value = next
  } catch (cause) {
    if (isCurrent(id, version)) error.value = cause.message
  }
}
function ask(type, item = null) {
  if (type === 'answer' && isAuthor.value && typeof soup.value.answer === 'string') {
    answer.value = soup.value.answer
    return
  }
  confirmType.value = type
  confirmItem.value = item
  confirmOpen.value = true
}

function confirmed() {
  const type = confirmType.value
  const item = confirmItem.value
  if (type === 'answer') showAnswer()
  if (type === 'comment' && item) showComment(item)
  if (type === 'delete') deleteSoup()
}

async function showAnswer() {
  if (isAuthor.value && typeof soup.value.answer === 'string') {
    answer.value = soup.value.answer
    return
  }
  const id = props.id
  const version = loadVersion
  try {
    const result = await send(`/api/soups/${id}/answer`, 'POST')
    if (isCurrent(id, version)) answer.value = result.answer
  } catch (cause) {
    if (isCurrent(id, version)) error.value = cause.message
  }
}
async function showComment(item) {
  const id = props.id
  const version = loadVersion
  try {
    const result = await send(`/api/soups/${id}/comments/${item.id}/spoiler`, 'POST')
    if (!isCurrent(id, version) || !comments.value.includes(item)) return
    item.body = result.body
    error.value = ''
  } catch (cause) {
    if (isCurrent(id, version)) error.value = cause.message
  }
}
async function addComment() {
  const body = comment.value.trim()
  if (!body || posting.value) return
  const id = props.id
  const version = loadVersion
  posting.value = true
  try {
    await send(`/api/soups/${id}/comments`, 'POST', { body, spoiler: spoiler.value })
    if (!isCurrent(id, version)) return
    comment.value = ''
    spoiler.value = false
    await loadComments(id, version)
    if (!isCurrent(id, version)) return
    const nextSoup = await request(`/api/soups/${id}`)
    if (isCurrent(id, version)) soup.value = nextSoup
  } catch (cause) {
    if (isCurrent(id, version)) error.value = cause.message
  } finally {
    posting.value = false
  }
}
// 删除由服务端校验作者身份，并保留已有对局的题目快照。
async function deleteSoup() {
  if (deleting.value) return
  deleting.value = true
  error.value = ''
  try {
    await send(`/api/soups/${props.id}`, 'DELETE')
    emit('deleted')
  } catch (cause) { error.value = cause.message }
  finally { deleting.value = false }
}
watch(() => [props.id, props.user?.id], load)
onMounted(load)
onUnmounted(() => { loadVersion++ })
</script>
<template>
  <main class="game-ui game-detail">
    <div class="game-width">
      <button class="game-back" @click="emit('back')"><span aria-hidden="true">←</span> 返回</button>
      <h1 class="game-title">题目详情</h1>
      <p v-if="loading" class="game-feed-state" role="status">正在加载题目…</p>
      <div v-else-if="!soup && error" class="game-feed-state" role="alert"><p>{{ error }}</p><button class="game-stamp game-stamp--paper game-stamp--small" @click="load">重试</button></div>
      <div v-if="soup" class="game-detail-grid">
        <div>
          <article class="game-paper-panel game-detail-story">
            <i class="game-corner-tape" aria-hidden="true"></i>
            <span class="game-case-label">CASE / {{ String(soup.id).padStart(3, '0') }}</span>
            <h2>{{ soup.title }}</h2>
            <div class="game-tags"><span class="game-tag game-tag--teal">{{ soup.genre }}</span><span class="game-tag">难度 {{ soup.difficulty }}/5</span><span class="game-tag">恐怖 {{ soup.horror }}/5</span></div>
            <p class="game-detail-byline"><button class="game-text-link" @click="emit('profile', soup.authorId)">@{{ soup.author }}</button> 发布</p>
            <p v-if="soup.hidden" class="game-detail-notice" role="status">题目已隐藏，仅作者可查看；不能互动或用于新对局。</p>
            <div class="game-detail-surface">{{ soup.surface }}</div>
            <div v-if="!soup.hidden && user" class="game-detail-actions">
              <button class="game-quiet" :aria-pressed="!!soup.liked" @click="toggle('like', soup.liked)">{{ soup.liked ? '♥ 已点赞' : '♡ 点赞' }} {{ soup.likes }}</button>
              <button class="game-quiet" :aria-pressed="!!soup.bookmarked" @click="toggle('bookmark', soup.bookmarked)">{{ soup.bookmarked ? '★ 已收藏' : '☆ 收藏' }} {{ soup.bookmarks }}</button>
            </div>
            <div v-if="!soup.hidden && user && !answer" class="game-detail-spoiler"><span>汤底不会在普通浏览中显示。查看后将失去这道题的玩家资格。</span><button class="game-stamp game-stamp--paper game-stamp--small" @click="ask('answer')">查看汤底 <span aria-hidden="true">↗</span></button></div>
            <div v-if="isAuthor" class="game-detail-author-actions">
              <button v-if="soup.hidden && !answer" class="game-text-link" @click="ask('answer')">查看汤底</button>
              <button v-if="!soup.hidden" class="game-text-link" :disabled="deleting" @click="emit('edit', soup.id)">编辑题目</button>
              <button class="game-text-link" :disabled="deleting" @click="ask('delete')">{{ deleting ? '正在删除…' : '删除题目' }}</button>
            </div>
            <p v-if="!user" class="game-detail-hint">登录后可互动或查看汤底。</p>
          </article>

          <section v-if="answer" class="game-paper-panel game-answer"><h3>汤底</h3><p>{{ answer }}</p></section>

          <section v-if="!soup.hidden" class="game-paper-panel game-comments">
            <h3>评论 <span class="game-tag">{{ soup.comments }}</span></h3>
            <p class="game-detail-hint">评论可能包含汤底，需主动确认后查看。</p>
            <form v-if="user" class="game-comment-form" @submit.prevent="addComment">
              <label class="game-field"><span class="game-visually-hidden">发表评论</span><textarea v-model="comment" rows="2" maxlength="500" placeholder="写下你的想法" required></textarea></label>
              <div class="game-comment-controls"><label class="game-check"><input v-model="spoiler" type="checkbox" />这条评论包含剧透</label><button class="game-stamp game-stamp--teal game-stamp--small" :disabled="posting">{{ posting ? '正在发表…' : '发表评论' }}</button></div>
            </form>
            <p v-if="commentsBusy && !comments.length" class="game-detail-hint" role="status">正在加载评论…</p>
            <p v-else-if="!comments.length && !commentsError" class="game-detail-hint">还没有评论。</p>
            <div class="game-comment-list">
              <article v-for="item in comments" :key="item.id" class="game-comment">
                <span class="game-comment-avatar" aria-hidden="true">{{ item.author.slice(0, 1) }}</span>
                <div><button class="game-text-link" @click="emit('profile', item.authorId)">@{{ item.author }}</button>
                  <p v-if="!item.body">{{ item.spoiler ? '剧透评论' : '评论内容已折叠' }} <button v-if="user" class="game-text-link" @click="ask('comment', item)">查看</button><span v-else> · 登录后可查看</span></p>
                  <p v-else>{{ item.body }}</p>
                </div>
              </article>
            </div>
            <div v-if="commentsError" class="game-feed-error" role="alert"><p>{{ commentsError }}</p><button v-if="!comments.length" class="game-stamp game-stamp--paper game-stamp--small" @click="loadComments(props.id, loadVersion)">重试加载评论</button></div>
            <button v-if="hasMoreComments" class="game-stamp game-stamp--paper game-stamp--small" :disabled="commentsBusy" @click="loadMoreComments">{{ commentsBusy ? '正在加载…' : '加载更多评论' }}</button>
          </section>
          <p v-if="error" class="game-feed-error" role="alert">{{ error }}</p>
        </div>
        <aside class="game-detail-art" aria-label="证物插画">
          <img :src="evidenceImage" alt="雨夜的照片与放大镜" width="1536" height="1024" loading="lazy" decoding="async" />
        </aside>
      </div>
    </div>
    <ConfirmDialog v-model:open="confirmOpen" :title="confirmTitle" :message="confirmMessage" :confirm-text="confirmText" :cancel-text="confirmType === 'delete' ? '保留题目' : confirmType === 'comment' ? '取消' : '继续推理'" :danger="confirmType === 'delete'" @confirm="confirmed" />
  </main>
</template>
