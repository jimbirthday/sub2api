<template>
  <BaseDialog :show="show" :title="t('admin.modelBans.catalog')" width="wide" :close-on-escape="!saving" :show-close-button="!saving" @close="!saving && emit('close')">
    <div class="space-y-4">
      <p class="text-sm text-gray-500">{{ t('admin.modelBans.catalogHint') }}</p>
      <p v-if="error" role="alert" class="text-sm text-red-600">{{ error }}</p>
      <section v-if="conflictValue" class="space-y-3 rounded-lg border border-amber-300 p-3 dark:border-amber-700">
        <p class="text-sm">{{ t('admin.modelBans.conflictReview') }}</p>
        <details><summary class="cursor-pointer text-sm">{{ t('admin.modelBans.compareVersions') }}</summary><div class="mt-2 grid gap-3 sm:grid-cols-2"><div><p class="input-label">{{ t('admin.modelBans.yourDraft') }}</p><pre class="max-h-48 overflow-auto whitespace-pre-wrap break-all text-xs">{{ JSON.stringify(value, null, 2) }}</pre></div><div><p class="input-label">{{ t('admin.modelBans.latestSaved') }}</p><pre class="max-h-48 overflow-auto whitespace-pre-wrap break-all text-xs">{{ JSON.stringify(conflictValue, null, 2) }}</pre></div></div></details>
        <button class="btn btn-secondary btn-sm" :disabled="saving" @click="confirmReload = true">{{ t('admin.modelBans.reloadLatest') }}</button>
      </section>
      <label class="block"><span class="input-label">{{ t('admin.modelBans.changeReason') }}</span><input v-model="reason" class="input" required maxlength="2000" :disabled="saving" /></label>
      <p v-if="loading">{{ t('common.loading') }}</p>
      <template v-else-if="value">
        <p class="text-xs text-gray-500">{{ t('admin.modelBans.revision', { revision: value.revision }) }}</p>
        <div class="flex items-center justify-between"><h4 class="font-medium">{{ t('admin.modelBans.models') }}</h4><button class="btn btn-secondary btn-sm" :disabled="saving" @click="value.models.push({ namespace: '', canonical_model: '', display_name: '', source: 'custom' })">{{ t('admin.modelBans.add') }}</button></div>
        <section v-for="(model, index) in value.models" :key="index" class="rounded-lg border border-gray-200 p-3 dark:border-dark-600">
          <div class="grid gap-3 sm:grid-cols-2">
            <label><span class="input-label">{{ t('admin.modelBans.provider') }}</span><input v-model="model.namespace" class="input" :disabled="saving" /></label>
            <label><span class="input-label">{{ t('admin.modelBans.model') }}</span><input v-model="model.canonical_model" class="input" :disabled="saving" /></label>
            <label><span class="input-label">{{ t('admin.modelBans.displayName') }}</span><input v-model="model.display_name" class="input" :disabled="saving" /></label>
            <div class="flex items-end justify-end"><button class="btn btn-secondary btn-sm text-red-600" :disabled="saving" @click="value.models.splice(index, 1)">{{ t('common.delete') }}</button></div>
          </div>
        </section>
        <div class="flex items-center justify-between border-t pt-4 dark:border-dark-600"><h4 class="font-medium">{{ t('admin.modelBans.aliases') }}</h4><button class="btn btn-secondary btn-sm" :disabled="saving" @click="value.aliases.push({ namespace: '', transport: '', alias: '', canonical_model: '' })">{{ t('admin.modelBans.add') }}</button></div>
        <section v-for="(alias, index) in value.aliases" :key="index" class="rounded-lg border border-gray-200 p-3 dark:border-dark-600">
          <div class="grid gap-3 sm:grid-cols-2">
            <label><span class="input-label">{{ t('admin.modelBans.provider') }}</span><input v-model="alias.namespace" class="input" :disabled="saving" /></label>
            <label><span class="input-label">{{ t('admin.modelBans.transport') }}</span><input v-model="alias.transport" class="input" :disabled="saving" /></label>
            <label><span class="input-label">{{ t('admin.modelBans.alias') }}</span><input v-model="alias.alias" class="input" :disabled="saving" /></label>
            <label><span class="input-label">{{ t('admin.modelBans.model') }}</span><input v-model="alias.canonical_model" class="input" :disabled="saving" /></label>
            <label><span class="input-label">{{ t('admin.modelBans.protocol') }}</span><input v-model="alias.protocol" class="input" :disabled="saving" /></label>
            <label><span class="input-label">{{ t('admin.modelBans.role') }}</span><input v-model="alias.role" class="input" :disabled="saving" /></label>
            <label><span class="input-label">{{ t('admin.modelBans.providerContext') }}</span><input v-model="alias.provider_context" class="input" :disabled="saving" /></label>
          </div>
          <div class="mt-3 text-right"><button class="btn btn-secondary btn-sm text-red-600" :disabled="saving" @click="value.aliases.splice(index, 1)">{{ t('common.delete') }}</button></div>
        </section>
      </template>
    </div>
    <template #footer><button class="btn btn-secondary" :disabled="saving" @click="emit('close')">{{ t('common.cancel') }}</button><button class="btn btn-primary" :disabled="saving || loading || !value || !!conflictValue" @click="save">{{ saving ? t('common.saving') : t('common.save') }}</button></template>
  </BaseDialog>
  <ConfirmDialog :show="confirmReload" :title="t('admin.modelBans.reloadLatest')" :message="t('admin.modelBans.discardDraftConfirm')" danger @cancel="confirmReload = false" @confirm="reloadLatest" />
</template>
<script setup lang="ts">
import { ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import { catalog, replaceCatalog, type ModelCatalog } from '@/api/admin/modelRestrictions'
import { useAppStore } from '@/stores/app'
const props = defineProps<{ show: boolean }>()
const emit = defineEmits<{ close: [] }>()
const { t } = useI18n(), app = useAppStore()
const value = ref<ModelCatalog | null>(null), loading = ref(false), saving = ref(false), error = ref(''), reason = ref('')
const conflictValue = ref<ModelCatalog | null>(null), confirmReload = ref(false)
let generation = 0
watch(() => props.show, async show => {
  const request = ++generation
  if (!show) return
  conflictValue.value = null; confirmReload.value = false
  reason.value = ''; loading.value = true; value.value = null; error.value = ''
  try { const result = await catalog(); if (request === generation) value.value = result }
  catch { if (request === generation) error.value = t('admin.modelBans.failed') }
  finally { if (request === generation) loading.value = false }
}, { immediate: true })
async function save() {
  if (!value.value || saving.value) return
  if (!reason.value.trim()) { error.value = t('admin.modelBans.reasonRequired'); return }
  const request = generation
  saving.value = true; error.value = ''
  try { const result = await replaceCatalog(value.value, reason.value.trim()); if (request !== generation) return; value.value = result; app.showSuccess(t('admin.modelBans.saved')) }
  catch (err) {
    if (request === generation) {
      const conflict = ((err as { response?: { status?: number }; status?: number })?.response?.status ?? (err as { status?: number })?.status) === 409
      error.value = conflict ? t('admin.modelBans.conflict') : t('admin.modelBans.failed')
      if (conflict) { try { const latest = await catalog(); if (request === generation) conflictValue.value = latest } catch { /* Keep the draft on reload failure. */ } }
    }
  }
  finally { saving.value = false }
}
function reloadLatest() {
  confirmReload.value = false
  if (!conflictValue.value) return
  value.value = conflictValue.value; conflictValue.value = null; error.value = ''; reason.value = ''
}
</script>
