import { render, screen } from '@testing-library/react'
import { Position, ReactFlowProvider } from '@xyflow/react'
import { describe, expect, it } from 'vitest'
import { LanoryNode } from '@/features/topology/LanoryNode'
import { t } from '@/i18n'

describe('LanoryNode', () => {
  it('shows the detailed address summary and multi-subnet marker', () => {
    render(
      <ReactFlowProvider>
        <LanoryNode
        id="router"
        type="lanory"
        data={{
          kind: 'device',
          icon: 'router',
          label: 'Router',
          state: 'online',
          showDetail: true,
          addressLine: 'LAN 192.168.100.1 · WAN 192.168.1.4',
          multiHomed: true,
        }}
        selected={false}
        isConnectable={false}
        positionAbsoluteX={0}
        positionAbsoluteY={0}
        draggable={false}
        dragging={false}
        selectable={false}
        deletable={false}
        zIndex={0}
        targetPosition={Position.Top}
        sourcePosition={Position.Bottom}
        />
      </ReactFlowProvider>,
    )

    expect(screen.getByText('LAN 192.168.100.1 · WAN 192.168.1.4')).toBeTruthy()
    expect(screen.getByText(t('topology.multi_homed'))).toHaveAttribute('title', t('topology.multi_homed_hint'))
  })

  it('does not add address content at low zoom', () => {
    render(
      <ReactFlowProvider>
        <LanoryNode
        id="router"
        type="lanory"
        data={{
          kind: 'device',
          icon: 'router',
          label: 'Router',
          state: 'online',
          showDetail: false,
          addressLine: 'LAN 192.168.100.1 · WAN 192.168.1.4',
          multiHomed: true,
        }}
        selected={false}
        isConnectable={false}
        positionAbsoluteX={0}
        positionAbsoluteY={0}
        draggable={false}
        dragging={false}
        selectable={false}
        deletable={false}
        zIndex={0}
        targetPosition={Position.Top}
        sourcePosition={Position.Bottom}
        />
      </ReactFlowProvider>,
    )

    expect(screen.queryByText('LAN 192.168.100.1 · WAN 192.168.1.4')).toBeNull()
    expect(screen.queryByText(t('topology.multi_homed'))).toBeNull()
  })
})
