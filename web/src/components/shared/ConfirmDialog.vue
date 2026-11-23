<script setup>
// 统一承载会改变对局或透露汤底的确认操作。
import { nextTick, onUnmounted, ref, useId, watch } from 'vue'

const props = defineProps({
  open: { type: Boolean, default: false },
  title: { type: String, required: true },
  message: { type: String, required: true },
  label: { type: String, default: '确认操作' },
  confirmText: { type: String, default: '确定' },
  cancelText: { type: String, default: '取消' },
  danger: { type: Boolean, default: false },
})
const emit = defineEmits(['update:open', 'confirm', 'cancel'])
const panel = ref(null)
const titleId = useId()
const messageId = `${titleId}-message`
let previousFocus = null

watch(() => props.open, async value => {
  if (value) {
    previousFocus = document.activeElement
    await nextTick()
    panel.value?.querySelector('[data-cancel], .confirm-button')?.focus()
  } else {
    await nextTick()
    if (previousFocus?.isConnected) previousFocus.focus()
    previousFocus = null
  }
}, { immediate: true })

onUnmounted(() => {
  if (previousFocus?.isConnected) previousFocus.focus()
})

function cancel() {
  emit('update:open', false)
  emit('cancel')
}

function confirm() {
  emit('update:open', false)
  emit('confirm')
}

function keydown(event) {
  if (event.key === 'Escape') {
    event.preventDefault()
    cancel()
    return
  }
  if (event.key !== 'Tab') return
  const buttons = [...(panel.value?.querySelectorAll('button') || [])]
  if (!buttons.length) return
  const first = buttons[0]
  const last = buttons.at(-1)
  if (event.shiftKey && document.activeElement === first) {
    event.preventDefault()
    last.focus()
  } else if (!event.shiftKey && document.activeElement === last) {
    event.preventDefault()
    first.focus()
  }
}
</script>

<template>
  <Teleport to="body">
    <div v-if="open" class="confirm-shade" @click.self="cancel">
      <section ref="panel" class="confirm-sheet" role="dialog" aria-modal="true" :aria-labelledby="titleId" :aria-describedby="messageId" @keydown="keydown">
        <div class="confirm-symbol" aria-hidden="true">!</div>
        <p class="confirm-label">{{ label }}</p>
        <h2 :id="titleId">{{ title }}</h2>
        <p :id="messageId" class="confirm-message">{{ message }}</p>
        <div class="confirm-actions">
          <button v-if="cancelText" type="button" class="confirm-button confirm-button--quiet" data-cancel @click="cancel">{{ cancelText }}</button>
          <button type="button" class="confirm-button" :class="{ 'confirm-button--danger': danger }" @click="confirm">{{ confirmText }}</button>
        </div>
      </section>
    </div>
  </Teleport>
</template>
