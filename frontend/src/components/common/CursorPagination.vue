<template>
  <nav class="flex items-center justify-between gap-3" :aria-label="t('common.pagination')">
    <button class="btn btn-secondary" :disabled="loading || !hasPrevious" @click="$emit('previous')">{{ t('admin.modelBans.previous') }}</button>
    <button class="btn btn-secondary" :disabled="loading || !hasNext" @click="$emit('next')">{{ t('admin.modelBans.next') }}</button>
  </nav>
</template>
<script setup lang="ts">
import { useI18n } from 'vue-i18n'
defineProps<{ hasPrevious: boolean; hasNext: boolean; loading?: boolean }>()
defineEmits<{ previous: []; next: [] }>()
const { t } = useI18n()
</script>
