import { afterAll, beforeAll, describe, expect, it, vi } from 'vitest'
import { StrictMode, useState } from 'react'
import { fireEvent, render, screen } from '@testing-library/react'
import { Modal } from './ui'

/**
 * jsdom has no layout engine, so `offsetParent` is null for every element and
 * the modal's "skip what is not rendered" filter would drop the whole dialog.
 * Give jsdom a browser-like answer so the trap is actually exercised.
 */
const offsetParentDescriptor = Object.getOwnPropertyDescriptor(HTMLElement.prototype, 'offsetParent')

beforeAll(() => {
  Object.defineProperty(HTMLElement.prototype, 'offsetParent', {
    configurable: true,
    get() {
      const self = this as HTMLElement
      if (!self.isConnected) return null
      for (let node: HTMLElement | null = self; node; node = node.parentElement) {
        const style = window.getComputedStyle(node)
        if (style.display === 'none' || style.visibility === 'hidden') return null
      }
      return self.parentElement ?? document.body
    },
  })
})

afterAll(() => {
  if (offsetParentDescriptor) {
    Object.defineProperty(HTMLElement.prototype, 'offsetParent', offsetParentDescriptor)
  }
})

function Harness({ onClose }: { onClose?: () => void }) {
  const [open, setOpen] = useState(false)
  return (
    <>
      <button onClick={() => setOpen(true)}>open dialog</button>
      <Modal
        open={open}
        onClose={() => {
          onClose?.()
          setOpen(false)
        }}
        title="Add device"
        footer={<button>Save</button>}
      >
        <input aria-label="device name" />
        <button>Cancel</button>
      </Modal>
    </>
  )
}

/** Opens the harness dialog from its trigger and returns the dialog element. */
function openDialog(onClose?: () => void, wrap?: (ui: React.ReactElement) => React.ReactElement) {
  const ui = <Harness onClose={onClose} />
  render(wrap ? wrap(ui) : ui)
  const trigger = screen.getByText('open dialog')
  trigger.focus()
  fireEvent.click(trigger)
  return { trigger, dialog: screen.getByRole('dialog') }
}

describe('Modal', () => {
  it('moves focus into the dialog when it opens', () => {
    const { dialog } = openDialog()

    expect(dialog.contains(document.activeElement)).toBe(true)
    // The first focusable descendant is the close button in the header.
    expect(document.activeElement).toBe(screen.getByLabelText('close'))
  })

  it('wraps Tab from the last focusable element to the first', () => {
    const { dialog } = openDialog()

    const last = screen.getByText('Save')
    last.focus()
    fireEvent.keyDown(last, { key: 'Tab' })

    expect(document.activeElement).toBe(screen.getByLabelText('close'))
    expect(dialog.contains(document.activeElement)).toBe(true)
  })

  it('wraps Shift+Tab from the first focusable element to the last', () => {
    const { dialog } = openDialog()

    const first = screen.getByLabelText('close')
    first.focus()
    fireEvent.keyDown(first, { key: 'Tab', shiftKey: true })

    expect(document.activeElement).toBe(screen.getByText('Save'))
    expect(dialog.contains(document.activeElement)).toBe(true)
  })

  it('pulls focus back when it has escaped the dialog', () => {
    const { dialog } = openDialog()

    const outside = document.body
    outside.focus()
    fireEvent.keyDown(outside, { key: 'Tab' })

    expect(document.activeElement).toBe(screen.getByLabelText('close'))
    expect(dialog.contains(document.activeElement)).toBe(true)
  })

  it('does not trap Tab inside the dialog for ordinary moves', () => {
    openDialog()

    const input = screen.getByLabelText('device name')
    input.focus()
    fireEvent.keyDown(input, { key: 'Tab' })

    // jsdom does not move focus on Tab by itself, so the only thing this proves
    // is that the handler left a mid-dialog element alone instead of wrapping.
    expect(document.activeElement).toBe(input)
  })

  it('calls onClose on Escape', () => {
    const onClose = vi.fn()
    openDialog(onClose)

    fireEvent.keyDown(document.activeElement as HTMLElement, { key: 'Escape' })

    expect(onClose).toHaveBeenCalledTimes(1)
  })

  it('restores focus to the trigger after closing', () => {
    const { trigger } = openDialog()
    expect(document.activeElement).not.toBe(trigger)

    fireEvent.keyDown(document.activeElement as HTMLElement, { key: 'Escape' })

    expect(screen.queryByRole('dialog')).toBeNull()
    expect(document.activeElement).toBe(trigger)
  })

  it('does not keep a trap or a key listener while closed', () => {
    const onClose = vi.fn()
    const { trigger } = openDialog(onClose)
    fireEvent.keyDown(document.activeElement as HTMLElement, { key: 'Escape' })

    // Escape after the dialog is gone must not reach onClose again, and Tab must
    // not be swallowed by a leftover document listener.
    fireEvent.keyDown(trigger, { key: 'Escape' })
    expect(onClose).toHaveBeenCalledTimes(1)

    const tab = fireEvent.keyDown(trigger, { key: 'Tab' })
    expect(tab).toBe(true)
  })

  // The app renders under StrictMode, which mounts, cleans up and mounts again.
  // The focus handoff has to survive that double invocation.
  it('keeps working under StrictMode', () => {
    const { trigger } = openDialog(undefined, (ui) => <StrictMode>{ui}</StrictMode>)
    expect(document.activeElement).toBe(screen.getByLabelText('close'))

    fireEvent.keyDown(document.activeElement as HTMLElement, { key: 'Escape' })
    expect(screen.queryByRole('dialog')).toBeNull()
    expect(document.activeElement).toBe(trigger)
  })
})
