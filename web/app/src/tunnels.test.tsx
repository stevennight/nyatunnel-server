import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { CSRF_HEADER } from './api'
import { adminRoutes, device, mockApi, tunnel } from './test/mockApi'
import type { Recorded } from './test/mockApi'
import { renderAt } from './test/render'
import type { TunnelInput } from './types'

const aliceDevice = device({ id: 'dev_alice', userId: 'usr_alice', username: 'alice', name: 'alice-pc' })

describe('tunnel form', () => {
  it('forces the "<username>-" subdomain prefix and user domains for a normal-user owner', async () => {
    const calls = mockApi({
      ...adminRoutes(),
      'GET /devices': { devices: [device(), aliceDevice, device({ id: 'dev_old', userId: 'usr_alice', name: 'old-pc', revoked: true })] },
      'POST /tunnels': { status: 201, body: { tunnel: tunnel() } },
    })
    renderAt('/tunnels')
    const user = userEvent.setup()

    await user.click(await screen.findByRole('button', { name: /新建隧道/ }))
    const form = await screen.findByRole('form', { name: '隧道表单' })

    // Owned by the admin: no prefix, every domain.
    await waitFor(() => expect(within(form).getByLabelText('所属用户')).toHaveValue('usr_admin'))
    expect(within(form).queryByTestId('subdomain-prefix')).not.toBeInTheDocument()
    expect(within(within(form).getByLabelText('域名')).getAllByRole('option').map((o) => o.textContent)).toEqual(['.dev.example.com', '.t.example.com'])

    await user.type(within(form).getByLabelText('子域名'), 'blog')
    await user.selectOptions(within(form).getByLabelText('所属用户'), 'usr_alice')

    // Owned by alice: the prefix is shown and kept out of the editable part.
    expect(within(form).getByTestId('subdomain-prefix')).toHaveTextContent('alice-')
    expect(within(form).getByLabelText('子域名')).toHaveValue('blog')
    expect(within(form).getByText(/子域名必须以/)).toBeInTheDocument()
    expect(within(form).getByText('https://alice-blog.dev.example.com')).toBeInTheDocument()
    expect(within(within(form).getByLabelText('域名')).getAllByRole('option').map((o) => o.textContent)).toEqual(['.dev.example.com'])
    // Devices: alice's non-revoked ones plus "not bound yet".
    expect(within(within(form).getByLabelText('绑定设备')).getAllByRole('option').map((o) => o.textContent)).toEqual([
      '暂不绑定（等待设备注册）',
      'alice-pc（在线）',
    ])

    await user.type(within(form).getByLabelText('名称'), 'alice-blog')
    await user.type(within(form).getByLabelText('本地端口'), '3000')
    await user.click(within(form).getByRole('button', { name: '创建' }))

    const post = await waitFor(() => {
      const found = calls.find((c: Recorded) => c.method === 'POST' && c.path === '/tunnels')
      expect(found).toBeTruthy()
      return found!
    })
    expect(post.headers[CSRF_HEADER]).toBe('1')
    const body = post.body as TunnelInput
    expect(body).toMatchObject({
      userId: 'usr_alice',
      deviceId: null,
      name: 'alice-blog',
      type: 'https',
      domainId: 'dom_1',
      subdomain: 'alice-blog',
      remotePort: null,
      localIp: '127.0.0.1',
      localPort: 3000,
      enabled: true,
      expiresAt: null,
    })
  })

  it('drops the prefix again when the owner becomes an administrator', async () => {
    mockApi(adminRoutes())
    renderAt('/tunnels')
    const user = userEvent.setup()
    await user.click(await screen.findByRole('button', { name: /新建隧道/ }))
    const form = await screen.findByRole('form', { name: '隧道表单' })
    await waitFor(() => expect(within(form).getByLabelText('所属用户')).toHaveValue('usr_admin'))
    await user.selectOptions(within(form).getByLabelText('所属用户'), 'usr_alice')
    await user.type(within(form).getByLabelText('子域名'), 'demo')
    expect(within(form).getByText('https://alice-demo.dev.example.com')).toBeInTheDocument()
    await user.selectOptions(within(form).getByLabelText('所属用户'), 'usr_admin')
    expect(within(form).queryByTestId('subdomain-prefix')).not.toBeInTheDocument()
    expect(within(form).getByLabelText('子域名')).toHaveValue('demo')
    expect(within(form).getByText('https://demo.dev.example.com')).toBeInTheDocument()
  })

  it('splits an existing user subdomain into prefix and editable part, and keeps the type fixed', async () => {
    const calls = mockApi({
      ...adminRoutes(),
      'GET /devices': { devices: [aliceDevice] },
      'GET /tunnels': {
        tunnels: [tunnel({ id: 'tun_a', userId: 'usr_alice', username: 'alice', subdomain: 'alice-photos', name: 'photos', deviceId: 'dev_alice', deviceName: 'alice-pc' })],
      },
      'PUT /tunnels/tun_a': { tunnel: tunnel() },
    })
    renderAt('/tunnels')
    const user = userEvent.setup()
    await user.click(await screen.findByRole('button', { name: '编辑' }))
    const form = await screen.findByRole('form', { name: '隧道表单' })
    await waitFor(() => expect(within(form).getByTestId('subdomain-prefix')).toHaveTextContent('alice-'))
    expect(within(form).getByLabelText('子域名')).toHaveValue('photos')
    expect(within(form).getByLabelText('类型')).toBeDisabled()

    await user.clear(within(form).getByLabelText('子域名'))
    await user.type(within(form).getByLabelText('子域名'), 'pics')
    await user.click(within(form).getByRole('button', { name: '保存并下发' }))
    const put = await waitFor(() => {
      const found = calls.find((c) => c.method === 'PUT')
      expect(found).toBeTruthy()
      return found!
    })
    expect(put.body).toMatchObject({ userId: 'usr_alice', deviceId: 'dev_alice', subdomain: 'alice-pics', type: 'https' })
  })

  it('sends null for an automatic TCP port and the number when one is given', async () => {
    const calls = mockApi({ ...adminRoutes(), 'POST /tunnels': { status: 201, body: { tunnel: tunnel() } } })
    renderAt('/tunnels')
    const user = userEvent.setup()
    await user.click(await screen.findByRole('button', { name: /新建隧道/ }))
    const form = await screen.findByRole('form', { name: '隧道表单' })
    await user.selectOptions(within(form).getByLabelText('类型'), 'tcp')
    await user.type(within(form).getByLabelText('名称'), 'ssh')
    await user.type(within(form).getByLabelText('本地端口'), '22')
    await user.click(within(form).getByRole('button', { name: '创建' }))
    await waitFor(() => expect(calls.some((c) => c.method === 'POST')).toBe(true))
    expect(calls.find((c) => c.method === 'POST')!.body).toMatchObject({ type: 'tcp', remotePort: null, domainId: null, subdomain: null, localPort: 22 })
  })

  it('explains that only the public port is automatic', async () => {
    const calls = mockApi({ ...adminRoutes(), 'POST /tunnels': { status: 201, body: { tunnel: tunnel() } } })
    renderAt('/tunnels')
    const user = userEvent.setup()
    await user.click(await screen.findByRole('button', { name: /新建隧道/ }))
    const form = await screen.findByRole('form', { name: '隧道表单' })
    await user.selectOptions(within(form).getByLabelText('类型'), 'tcpudp')
    expect(within(form).getByText(/留空则从 TCP 与 UDP 端口池自动分配/)).toBeInTheDocument()
    await user.type(within(form).getByLabelText('名称'), 'game')
    await user.click(within(form).getByRole('button', { name: '创建' }))
    expect(await within(form).findByText(/请填写本地端口/)).toBeInTheDocument()
    expect(calls.some((c) => c.method === 'POST')).toBe(false)
  })
})
