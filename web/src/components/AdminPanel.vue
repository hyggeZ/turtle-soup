<script setup>
// 管理员查询账号，并可停用或恢复普通账号。
import { onMounted, ref } from 'vue'
import { request, send } from '../api'
const users = ref([])
const query = ref('')
const error = ref('')
async function load() {
  try {
    users.value = await request(`/api/admin/users?q=${encodeURIComponent(query.value)}`)
    error.value = ''
  } catch (cause) {
    error.value = cause.message
  }
}
async function setDisabled(item) {
  try {
    await send(`/api/admin/users/${item.id}`, 'PATCH', { disabled: !item.disabled })
    await load()
  } catch (cause) {
    error.value = cause.message
  }
}
onMounted(load)
</script>
<template>
  <section class="panel"><h1>账号管理</h1>
    <form class="inline-form" @submit.prevent="load"><input v-model="query" placeholder="搜索用户名" /><button>搜索</button></form>
    <p v-if="error" role="alert">{{ error }}</p>
    <table class="simple-table"><thead><tr><th>ID</th><th>用户名</th><th>权限</th><th>状态</th><th>操作</th></tr></thead>
      <tbody><tr v-for="item in users" :key="item.id"><td>{{ item.id }}</td><td>{{ item.username }}</td><td>{{ item.isAdmin ? '管理员' : '用户' }}</td><td>{{ item.disabled ? '已停用' : '正常' }}</td><td><button v-if="!item.isAdmin" class="text-button" @click="setDisabled(item)">{{ item.disabled ? '恢复' : '停用' }}</button></td></tr></tbody>
    </table>
  </section>
</template>
