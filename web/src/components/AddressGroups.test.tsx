import { describe, expect, it } from 'vitest'
import { fireEvent, render, screen } from '@testing-library/react'
import { AddressGroups } from './AddressGroups'
import type { AddressGroup } from '@/lib/devices'

function groups(): AddressGroup[] {
  return [
    {
      cidr: '192.168.100.0/24',
      side: 'lan',
      addresses: [
        { address: '192.168.100.116', family: 'ipv4', side: 'lan', is_primary: true },
        { address: '2001:db8:100::1', family: 'ipv6', side: 'lan' },
        { address: '2001:db8:100::2', family: 'ipv6', side: 'lan' },
        { address: '2001:db8:100::3', family: 'ipv6', side: 'lan' },
      ],
    },
  ]
}

describe('AddressGroups', () => {
  it('keeps the first address block compact and reveals the rest on demand', () => {
    render(<AddressGroups groups={groups()} />)

    expect(screen.getByText('192.168.100.116')).toBeTruthy()
    expect(screen.getByText('2001:db8:100::1')).toBeTruthy()
    expect(screen.queryByText('2001:db8:100::2')).toBeNull()
    expect(screen.getByRole('button', { name: /显示全部 4 个地址|show all 4 addresses/i })).toBeTruthy()

    fireEvent.click(screen.getByRole('button', { name: /显示全部 4 个地址|show all 4 addresses/i }))
    expect(screen.getByText('2001:db8:100::2')).toBeTruthy()
    expect(screen.getByText('2001:db8:100::3')).toBeTruthy()
  })

  it('keeps long IPv6 addresses on one line in a horizontally scrollable row', () => {
    render(
      <AddressGroups
        groups={[{
          cidr: '',
          side: 'unknown',
          addresses: [{
            address: 'fd26:397c:65d0:0:1137:fb2f:c692:9257',
            family: 'ipv6',
            side: 'unknown',
          }],
        }]}
      />,
    )

    const address = screen.getByText('fd26:397c:65d0:0:1137:fb2f:c692:9257')
    expect(address.className).toContain('whitespace-nowrap')
    expect(address.className).toContain('overflow-x-auto')
  })

  it('does not label an address with an unresolved interface side as unknown', () => {
    render(
      <AddressGroups
        groups={[
          { cidr: '', side: 'unknown', addresses: [{ address: '2001:db8::1', family: 'ipv6', side: 'unknown' }] },
          { cidr: '', side: 'unknown', addresses: [{ address: '2001:db8::2', family: 'ipv6', side: 'unknown' }] },
        ]}
      />,
    )

    expect(screen.queryAllByText('Unknown')).toHaveLength(0)
    expect(screen.queryAllByText('未知')).toHaveLength(0)
  })
})
