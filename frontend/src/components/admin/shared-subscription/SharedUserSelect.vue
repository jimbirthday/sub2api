<template>
  <Select :model-value="modelValue" :options="options" remote clearable :loading="loading"
    :disabled="disabled" :aria-label="t('sharedSubscriptions.searchUsers')"
    :placeholder="t('sharedSubscriptions.searchUsers')" :search-placeholder="t('sharedSubscriptions.searchUsers')"
    @search="search" @update:model-value="select" />
  <p v-if="error" role="alert" class="mt-1 text-sm text-red-600 dark:text-red-400">{{ error }}</p>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import Select from '@/components/common/Select.vue'
import { getById, list } from '@/api/admin/users'
import { extractApiErrorMessage } from '@/utils/apiError'
import type { AdminUser } from '@/types'

const props = defineProps<{ modelValue?: number | null; disabled?: boolean; activeOnly?: boolean }>()
const emit = defineEmits<{ 'update:modelValue': [value: number | undefined] }>()
const { t } = useI18n()
const users = ref<AdminUser[]>([])
const selected = ref<AdminUser>()
const loading = ref(false)
const error = ref('')
let revision = 0
const options = computed(() => {
  const results = [...users.value]
  if (selected.value && !results.some(user => user.id === selected.value!.id)) results.unshift(selected.value)
  const mapped = results.map(user => ({ value: user.id, label: `${user.email}${user.username ? ` · ${user.username}` : ''} (#${user.id})` }))
  if (props.modelValue && !mapped.some(user => user.value === props.modelValue)) mapped.unshift({ value: props.modelValue, label: `#${props.modelValue}` })
  return mapped
})
async function search(query: string) {
  const current = ++revision
  error.value = ''
  if (!query.trim()) { users.value = []; loading.value = false; return }
  loading.value = true
  users.value = []
  try {
    const result = await list(1, 30, { search: query.trim(), status: props.activeOnly ? 'active' : undefined })
    if (current === revision) users.value = result.items
  } catch (err) {
    if (current === revision) error.value = extractApiErrorMessage(err) || t('common.error')
  } finally {
    if (current === revision) loading.value = false
  }
}
function select(value: string | number | boolean | null) {
  selected.value = users.value.find(user => user.id === value)
  emit('update:modelValue', typeof value === 'number' ? value : undefined)
}
let selectionRevision = 0
watch(() => props.modelValue, async id => {
  const current = ++selectionRevision
  if (!id) { selected.value = undefined; return }
  const known = users.value.find(user => user.id === id)
  if (known) { selected.value = known; return }
  if (selected.value?.id === id) return
  selected.value = undefined
  try {
    const user = await getById(id, !props.activeOnly)
    if (current === selectionRevision) selected.value = user
  } catch (err) {
    if (current === selectionRevision) error.value = extractApiErrorMessage(err) || t('common.error')
  }
}, { immediate: true })
onBeforeUnmount(() => { revision++; selectionRevision++ })
</script>
