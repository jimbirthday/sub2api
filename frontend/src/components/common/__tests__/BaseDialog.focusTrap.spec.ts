import { mount } from '@vue/test-utils'
import { nextTick } from 'vue'
import { describe, it, expect } from 'vitest'
import BaseDialog from '../BaseDialog.vue'

describe('BaseDialog focus containment', () => {
  it('cycles Tab in both directions and restores the opener', async () => {
    const opener = document.createElement('button')
    document.body.append(opener)
    opener.focus()
    const wrapper = mount(BaseDialog, {
      attachTo: document.body,
      props: { show: true, title: 'Policy', showCloseButton: false },
      slots: { default: '<input id="first-control" /><button id="last-control">Save</button>' },
    })
    await nextTick()
    const first = document.querySelector<HTMLInputElement>('#first-control')!
    const last = document.querySelector<HTMLButtonElement>('#last-control')!
    expect(document.activeElement).toBe(first)
    last.focus()
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Tab', bubbles: true, cancelable: true }))
    expect(document.activeElement).toBe(first)
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Tab', shiftKey: true, bubbles: true, cancelable: true }))
    expect(document.activeElement).toBe(last)
    await wrapper.setProps({ show: false })
    expect(document.activeElement).toBe(opener)
    wrapper.unmount(); opener.remove()
  })
})
