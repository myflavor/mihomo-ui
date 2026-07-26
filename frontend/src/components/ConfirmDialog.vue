<script setup>
// Shared confirm for actions that are cheap to trigger but annoying to undo.
// The caller owns the open state and supplies the wording; this only asks.
defineProps({
  open: { type: Boolean, default: false },
  // Neutral title and buttons on purpose: the slot already names the action, and
  // repeating it in all three places just fills the dialog with the same words.
  title: { type: String, default: '提示' },
  confirmLabel: { type: String, default: '确认' },
  // Solid red for anything that destroys data; primary reads calmer for the rest.
  danger: { type: Boolean, default: false },
  busy: { type: Boolean, default: false },
})
const emit = defineEmits(['confirm', 'cancel'])
</script>

<template>
  <Transition name="modal-fade">
    <div v-if="open" class="modal-mask" @click.self="emit('cancel')">
      <div class="modal modal-confirm">
        <h3>{{ title }}</h3>
        <p class="confirm-text"><slot /></p>
        <div class="modal-actions">
          <button class="btn btn-ghost" :disabled="busy" @click="emit('cancel')">取消</button>
          <button
            class="btn"
            :class="danger ? 'btn-danger-solid' : 'btn-primary'"
            :disabled="busy"
            @click="emit('confirm')"
          >
            <span v-if="busy" class="btn-inline-busy">
              <span class="spin" aria-hidden="true" />
              <span>{{ confirmLabel }}</span>
            </span>
            <span v-else>{{ confirmLabel }}</span>
          </button>
        </div>
      </div>
    </div>
  </Transition>
</template>
