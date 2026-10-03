import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { CSRF_HEADER } from './api'
import { adminRoutes, mockApi, tunnel, userRoutes } from './test/mockApi'
import { renderAt } from './test/render'

const ENROLLMENT = {
  code: 'K7QP-3XMD',
  url: 'nyatunnel://enroll?v=1&s=tunnel.example.com&c=K7QP-3XMD',
  cliCommand: 'nyatunnel enroll https://tunnel.example.com K7QP-3XMD',
}

describe('enrollment dialog', () => {
  it('lets an admin pick the user and preassign unbound tunnels, then shows code, countdown, QR and links', async () => {
    const calls = mockApi({
      ...adminRoutes(),
      'GET /tunnels': {
        tunnels: [
          tunnel(),
          tunnel({ id: 'tun_free', name: 'alice-demo', userId: 'usr_alice', username: 'alice', deviceId: null, deviceName: '', state: 'unassigned' }),
          tunnel({ id: 'tun_mine', name: 'nas', deviceId: null, deviceName: '', state: 'unassigned' }),
        ],
      },
      'POST /enrollments': () => ({ status: 201, body: { ...ENROLLMENT, expiresAt: Date.now() + 10 * 60_000 } }),
    })
    renderAt('/devices')
    const user = userEvent.setup()

    await user.click(await screen.findByRole('button', { name: /邀请新设备/ }))
    const dialog = await screen.findByRole('dialog', { name: '邀请新设备' })
    // Defaults to the admin themself: only their own unbound tunnel is offered.
    await waitFor(() => expect(within(dialog).getByRole('checkbox', { name: /nas/ })).toBeInTheDocument())
    expect(within(dialog).queryByRole('checkbox', { name: /alice-demo/ })).not.toBeInTheDocument()

    await waitFor(() => expect(within(dialog).getAllByRole('option', { name: /alice/ }).length).toBeGreaterThan(0))
    await user.selectOptions(within(dialog).getByLabelText('设备归属用户'), 'usr_alice')
    await user.click(within(dialog).getByRole('checkbox', { name: /alice-demo/ }))
    await user.type(within(dialog).getByLabelText('设备名（建议）'), 'alice-laptop')
    await user.selectOptions(within(dialog).getByLabelText('有效期'), '60')
    await user.click(within(dialog).getByRole('button', { name: '生成注册码' }))

    expect(await within(dialog).findByLabelText('注册码')).toHaveTextContent('K7QP-3XMD')
    expect(within(dialog).getByRole('img', { name: '注册链接二维码' })).toBeInTheDocument()
    expect(within(dialog).getByRole('link', { name: /打开客户端/ })).toHaveAttribute('href', ENROLLMENT.url)
    expect(within(dialog).getByLabelText('注册链接')).toHaveValue(ENROLLMENT.url)
    expect(within(dialog).getByLabelText('CLI 命令')).toHaveValue(ENROLLMENT.cliCommand)
    expect(within(dialog).getByRole('timer')).toHaveTextContent(/剩余 (10:00|09:5\d)/)
    expect(within(dialog).getByRole('button', { name: /复制链接/ })).toBeInTheDocument()
    expect(within(dialog).getByRole('button', { name: /复制命令/ })).toBeInTheDocument()

    const post = calls.find((c) => c.method === 'POST' && c.path === '/enrollments')!
    expect(post.headers[CSRF_HEADER]).toBe('1')
    expect(post.body).toEqual({ userId: 'usr_alice', deviceNameHint: 'alice-laptop', tunnelIds: ['tun_free'], ttlMinutes: 60 })
  })

  it('lets a normal user invite only for themself', async () => {
    const calls = mockApi({
      ...userRoutes(),
      'POST /enrollments': { status: 201, body: { ...ENROLLMENT, expiresAt: Date.now() - 1000 } },
    })
    renderAt('/devices')
    const user = userEvent.setup()
    await user.click(await screen.findByRole('button', { name: /邀请我的设备/ }))
    const dialog = await screen.findByRole('dialog', { name: '邀请新设备' })
    expect(within(dialog).queryByLabelText('设备归属用户')).not.toBeInTheDocument()
    await user.click(within(dialog).getByRole('button', { name: '生成注册码' }))
    expect(await within(dialog).findByRole('img', { name: '注册链接二维码' })).toBeInTheDocument()
    expect(within(dialog).getByRole('timer')).toHaveTextContent('已过期')
    const post = calls.find((c) => c.path === '/enrollments')!
    expect(post.body).toEqual({ deviceNameHint: '', tunnelIds: [], ttlMinutes: 10 })
  })
})
