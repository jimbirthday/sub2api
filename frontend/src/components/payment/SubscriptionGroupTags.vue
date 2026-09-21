<template>
  <ul class="space-y-1.5" :aria-label="ariaLabel">
    <li v-for="(group, index) in groups" :key="group.key" class="flex min-w-0">
      <span
        :title="group.name"
        :class="[
          'inline-flex max-w-full rounded-md border px-2 py-1 text-xs font-medium leading-4',
          'whitespace-normal break-words [overflow-wrap:anywhere]',
          tagClasses[index % tagClasses.length],
        ]"
      >
        {{ group.name }}
      </span>
    </li>
  </ul>
</template>

<script setup lang="ts">
import { computed } from 'vue'

const props = defineProps<{
  groupIds?: number[]
  groupNames?: Record<string, string>
  ariaLabel?: string
}>()

const tagClasses = [
  'border-teal-200 bg-teal-50 text-teal-800 dark:border-teal-800 dark:bg-teal-950/40 dark:text-teal-200',
  'border-sky-200 bg-sky-50 text-sky-800 dark:border-sky-800 dark:bg-sky-950/40 dark:text-sky-200',
  'border-violet-200 bg-violet-50 text-violet-800 dark:border-violet-800 dark:bg-violet-950/40 dark:text-violet-200',
  'border-amber-200 bg-amber-50 text-amber-800 dark:border-amber-800 dark:bg-amber-950/40 dark:text-amber-200',
  'border-rose-200 bg-rose-50 text-rose-800 dark:border-rose-800 dark:bg-rose-950/40 dark:text-rose-200',
]

const groups = computed(() => {
  const names = props.groupNames || {}
  const ids = props.groupIds?.length
    ? props.groupIds.map(String)
    : Object.keys(names)

  return [...new Set(ids)].map(id => ({
    key: id,
    name: names[id] || `#${id}`,
  }))
})
</script>
