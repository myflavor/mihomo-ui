<script setup>
// Shared confirm for actions that are cheap to trigger but annoying to undo.
// The caller owns the open state and supplies the wording; this only asks.
import { useMaskClose } from '../composables/useMaskClose'

defineProps({
  open: { type: Boolean, default: false },
  // Solid red for anything that destroys data; primary reads calmer for the rest.
  danger: { type: Boolean, default: false },
  busy: { type: Boolean, default: false },
})
const emit = defineEmits(['confirm', 'cancel'])
const mask = useMaskClose(() => emit('cancel'))
</script>

<template>
  <Transition name="modal-fade">
    <div v-if="open" class="modal-mask" @mousedown="mask.onMousedown" @click="mask.onClick">
      <div class="modal modal-confirm">
        <h3>提示</h3>
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
              <span>确认</span>
            </span>
            <span v-else>确认</span>
          </button>
        </div>
      </div>
    </div>
  </Transition>
</template>
