import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { bucketize } from './components/traffic-chart'
import { fromLocalInput } from './format'
import { browser, safeNext } from './next'
import { adminRoutes, boot, channel, device, domain, listed, me, mockApi, normalUser, quota, request, tunnel, userRoutes } from './test/mockApi'
import type { Recorded } from './test/mockApi'
import { renderAt } from './test/render'
import type { ChannelInput, Quota, RequestInput, TunnelInput } from './types'

const found = (calls: Recorded[], method: string, path: string) =>
  waitFor(() => {
    const c = calls.find((x) => x.method === method && x.path === path)
    expect(c).toBeTruthy()
    return c!
  })

async function openForm(button: RegExp | string) {
  const user = userEvent.setup()
  await user.click(await screen.findByRole('button', { name: button }))
  const form = await screen.findByRole('form', { name: '隧道表单' })
  return { user, form }
}

describe('tunnel form: access control and limits', () => {
  it('keeps a stored access password when the field is left empty and replaces it when typed', async () => {
    let n = 0
    const calls = mockApi({
      ...adminRoutes(),
      'GET /tunnels': { tunnels: [tunnel({ accessPolicy: 'password', hasPassword: true, monthBytes: 5 * 1024 ** 3, monthlyQuotaMb: 10240 })] },
      'PUT /tunnels/tun_1': () => {
        n++
        return { tunnel: tunnel() }
      },
    })
    renderAt('/tunnels')
    // The list shows the policy and the month's traffic against the quota.
    expect(await screen.findByText('访问密码', { selector: '.tag' })).toBeInTheDocument()
    expect(screen.getByText('5 GB')).toBeInTheDocument()
    expect(screen.getByText('/ 10 GB')).toBeInTheDocument()

    const { user, form } = await openForm('编辑')
    expect(within(form).getByLabelText('访问策略')).toHaveValue('password')
    const pass = within(form).getByLabelText('访问密码')
    expect(pass).toHaveValue('')
    expect(pass).toHaveAttribute('placeholder', '留空保持不变')
    expect(within(form).getByText('已设置')).toBeInTheDocument()

    await user.click(within(form).getByRole('button', { name: '保存并下发' }))
    const put = await found(calls, 'PUT', '/tunnels/tun_1')
    const body = put.body as TunnelInput
    expect(body.accessPolicy).toBe('password')
    expect('accessPassword' in body).toBe(false)
    expect(body.monthlyQuotaMb).toBe(10240)
    expect(n).toBe(1)
  })

  it('sends a newly typed password and validates it', async () => {
    const calls = mockApi({ ...adminRoutes(), 'GET /tunnels': { tunnels: [tunnel({ accessPolicy: 'password', hasPassword: true })] }, 'PUT /tunnels/tun_1': { tunnel: tunnel() } })
    renderAt('/tunnels')
    const { user, form } = await openForm('编辑')
    await user.type(within(form).getByLabelText('访问密码'), 'abc')
    await user.click(within(form).getByRole('button', { name: '保存并下发' }))
    expect(await within(form).findByRole('alert')).toHaveTextContent('访问密码至少 6 个字符')
    await user.type(within(form).getByLabelText('访问密码'), 'defgh')
    await user.click(within(form).getByRole('button', { name: '保存并下发' }))
    const put = await found(calls, 'PUT', '/tunnels/tun_1')
    expect(put.body).toMatchObject({ accessPolicy: 'password', accessPassword: 'abcdefgh' })
  })

  it('requires a password for a new password / Basic policy and sends Basic settings', async () => {
    const calls = mockApi({ ...adminRoutes(), 'POST /tunnels': { status: 201, body: { tunnel: tunnel() } } })
    renderAt('/tunnels')
    const { user, form } = await openForm(/新建隧道/)
    await user.type(within(form).getByLabelText('名称'), 'nas')
    await user.type(within(form).getByLabelText('子域名'), 'nas')
    await user.type(within(form).getByLabelText('本地端口'), '5000')
    await user.selectOptions(within(form).getByLabelText('访问策略'), 'basic')
    expect(within(form).getByLabelText('访问密码')).toHaveAttribute('placeholder', '至少 6 个字符')
    expect(within(form).queryByText('已设置')).not.toBeInTheDocument()
    await user.click(within(form).getByRole('button', { name: '创建' }))
    expect(await within(form).findByRole('alert')).toHaveTextContent('请设置访问密码')

    await user.type(within(form).getByLabelText('访问密码'), 'secret-pw')
    await user.click(within(form).getByRole('button', { name: '创建' }))
    expect(await within(form).findByRole('alert')).toHaveTextContent('Basic 认证用户名')
    await user.type(within(form).getByLabelText('Basic 用户名'), 'family')
    await user.type(within(form).getByLabelText('IP 白名单'), '10.0.0.0/8，203.0.113.4')
    await user.click(within(form).getByLabelText(/首次访问显示风险提示页/))
    await user.type(within(form).getByLabelText('Host 头改写'), 'localhost:5000')
    await user.click(within(form).getByRole('button', { name: '创建' }))
    const post = await found(calls, 'POST', '/tunnels')
    expect(post.body).toMatchObject({
      accessPolicy: 'basic',
      accessPassword: 'secret-pw',
      basicUsername: 'family',
      ipAllowlist: '10.0.0.0/8, 203.0.113.4',
      interstitial: true,
      hostRewrite: 'localhost:5000',
      bandwidthKbps: 0,
      maxConns: 0,
      monthlyQuotaMb: 0,
      quotaAction: 'pause',
    })
  })

  it('sets who may pass a login gate', async () => {
    const calls = mockApi({
      ...adminRoutes(),
      'GET /tunnels': { tunnels: [tunnel({ accessPolicy: 'login', loginAccess: 'users', loginUsers: ['bob'] })] },
      'PUT /tunnels/tun_1': { tunnel: tunnel() },
    })
    renderAt('/tunnels')
    const { user, form } = await openForm('编辑')
    expect(within(form).getByLabelText('允许谁访问')).toHaveValue('users')
    const names = within(form).getByLabelText('允许访问的用户名')
    expect(names).toHaveValue('bob')
    await user.clear(names)
    await user.click(within(form).getByRole('button', { name: '保存并下发' }))
    expect(await within(form).findByRole('alert')).toHaveTextContent('请填写允许访问的用户名')
    await user.type(names, 'Bob， carol')
    await user.click(within(form).getByRole('button', { name: '保存并下发' }))
    const put = await found(calls, 'PUT', '/tunnels/tun_1')
    expect(put.body).toMatchObject({ accessPolicy: 'login', loginAccess: 'users', loginUsers: ['bob', 'carol'] })

    // Saving closes the form; open it again and narrow the scope to the owner.
    calls.length = 0
    const again = await openForm('编辑')
    await again.user.selectOptions(within(again.form).getByRole('combobox', { name: '允许谁访问' }), 'owner')
    expect(within(again.form).queryByLabelText('允许访问的用户名')).not.toBeInTheDocument()
    await again.user.click(within(again.form).getByRole('button', { name: '保存并下发' }))
    const put2 = await found(calls, 'PUT', '/tunnels/tun_1')
    expect(put2.body).toMatchObject({ loginAccess: 'owner', loginUsers: [] })
  })

  it('hides the HTTPS-only options for TCP and converts the limits', async () => {
    const calls = mockApi({ ...adminRoutes(), 'POST /tunnels': { status: 201, body: { tunnel: tunnel() } } })
    renderAt('/tunnels')
    const { user, form } = await openForm(/新建隧道/)
    await user.selectOptions(within(form).getByLabelText('访问策略'), 'login')
    await user.selectOptions(within(form).getByLabelText('类型'), 'tcp')

    const policy = within(form).getByLabelText('访问策略')
    expect(within(policy).getAllByRole('option').map((o) => o.getAttribute('value'))).toEqual(['public'])
    expect(policy).toHaveValue('public')
    expect(within(form).queryByLabelText('访问密码')).not.toBeInTheDocument()
    expect(within(form).queryByLabelText(/首次访问显示风险提示页/)).not.toBeInTheDocument()
    expect(within(form).queryByLabelText('Host 头改写')).not.toBeInTheDocument()
    expect(within(form).getByLabelText('IP 白名单')).toBeInTheDocument()

    await user.type(within(form).getByLabelText('名称'), 'mc')
    await user.type(within(form).getByLabelText('本地端口'), '25565')
    await user.type(within(form).getByLabelText('带宽上限'), '20')
    await user.type(within(form).getByLabelText('最大并发连接'), '200')
    await user.type(within(form).getByLabelText('月流量配额'), '1.5')
    await user.selectOptions(within(form).getByLabelText('超出配额后'), 'alert')
    await user.click(within(form).getByRole('button', { name: '创建' }))
    const post = await found(calls, 'POST', '/tunnels')
    expect(post.body).toMatchObject({
      type: 'tcp',
      accessPolicy: 'public',
      interstitial: false,
      hostRewrite: '',
      bandwidthKbps: 20000,
      maxConns: 200,
      monthlyQuotaMb: 1536,
      quotaAction: 'alert',
    })
  })

  it('offers approved custom domains of the owner and drops the subdomain for them', async () => {
    const calls = mockApi({
      ...adminRoutes(),
      'GET /domains': {
        domains: [
          domain(),
          domain({ id: 'dom_c1', name: 'blog.alice.me', kind: 'custom', ownerUserId: 'usr_alice', ownerName: 'alice', status: 'active' }),
          domain({ id: 'dom_c2', name: 'pending.alice.me', kind: 'custom', ownerUserId: 'usr_alice', status: 'pending' }),
          domain({ id: 'dom_c3', name: 'shop.bob.me', kind: 'custom', ownerUserId: 'usr_bob', status: 'active' }),
        ],
        publicIps: [],
      },
      'POST /tunnels': { status: 201, body: { tunnel: tunnel() } },
    })
    renderAt('/tunnels')
    const { user, form } = await openForm(/新建隧道/)
    await waitFor(() => expect(within(form).getByLabelText('所属用户')).toHaveValue('usr_admin'))
    await user.selectOptions(within(form).getByLabelText('所属用户'), 'usr_alice')
    const options = () => within(within(form).getByLabelText('域名')).getAllByRole('option').map((o) => o.textContent)
    expect(options()).toEqual(['.dev.example.com', 'blog.alice.me（自定义域名）'])

    await user.selectOptions(within(form).getByLabelText('域名'), 'dom_c1')
    expect(within(form).queryByLabelText('子域名')).not.toBeInTheDocument()
    expect(within(form).getByText('https://blog.alice.me')).toBeInTheDocument()
    await user.type(within(form).getByLabelText('名称'), 'blog')
    await user.type(within(form).getByLabelText('本地端口'), '8080')
    await user.click(within(form).getByRole('button', { name: '创建' }))
    const post = await found(calls, 'POST', '/tunnels')
    expect(post.body).toMatchObject({ userId: 'usr_alice', type: 'https', domainId: 'dom_c1', subdomain: null })
  })
})

describe('self-service tunnels for normal users', () => {
  it('turns a refused creation into a prefilled request', async () => {
    const calls = mockApi({
      ...userRoutes(),
      'POST /tunnels': { status: 403, body: { error: 'tunnel_limit', message: '隧道数量已达到额度上限，请提交申请' } },
      'POST /requests': { status: 201, body: { id: 'req_9' } },
    })
    renderAt('/tunnels')
    const { user, form } = await openForm(/新建隧道/)
    expect(within(form).queryByLabelText('所属用户')).not.toBeInTheDocument()
    expect(await within(form).findByTestId('subdomain-prefix')).toHaveTextContent('alice-')
    await user.type(within(form).getByLabelText('名称'), 'alice-blog')
    await user.type(within(form).getByLabelText('子域名'), 'blog')
    await user.type(within(form).getByLabelText('本地端口'), '3000')
    await user.click(within(form).getByRole('button', { name: '创建' }))

    expect(await within(form).findByText('隧道数量已达到额度上限，请提交申请')).toBeInTheDocument()
    await user.click(within(form).getByRole('button', { name: '改为提交申请' }))

    const rq = await screen.findByRole('form', { name: '申请表单' })
    expect(within(rq).getByLabelText('子域名')).toHaveValue('blog')
    expect(within(rq).getByLabelText('本地端口')).toHaveValue('3000')
    expect(within(rq).getByLabelText('名称')).toHaveValue('alice-blog')
    await user.type(within(rq).getByLabelText('理由'), '博客')
    await user.click(within(rq).getByRole('button', { name: '提交申请' }))
    const post = await found(calls, 'POST', '/requests')
    expect(post.body as RequestInput).toEqual({
      type: 'https',
      name: 'alice-blog',
      domainId: 'dom_1',
      subdomain: 'alice-blog',
      localIp: '127.0.0.1',
      localPort: 3000,
      durationHours: 0,
      reason: '博客',
    })
    expect(await screen.findByText(/申请已提交/)).toBeInTheDocument()
  })

  it('lists own requests and can withdraw a pending one', async () => {
    const calls = mockApi({
      ...userRoutes(),
      'GET /requests': {
        requests: [request(), request({ id: 'req_2', status: 'rejected', reviewNote: '请使用已有的隧道', reviewedAt: Date.now() - 1000 })],
        pending: 1,
      },
      'POST /requests/req_1/cancel': { ok: true },
    })
    renderAt('/requests')
    const user = userEvent.setup()
    expect(await screen.findByRole('heading', { name: '我的申请' })).toBeInTheDocument()
    expect(await screen.findByText(/请使用已有的隧道/)).toBeInTheDocument()
    expect(screen.getAllByText('alice-demo.dev.example.com').length).toBeGreaterThan(0)
    await user.click(screen.getByRole('button', { name: '撤回' }))
    await user.click(within(await screen.findByRole('dialog', { name: '撤回申请' })).getByRole('button', { name: '撤回' }))
    await found(calls, 'POST', '/requests/req_1/cancel')
  })
})

describe('request approval', () => {
  it('opens the tunnel form prefilled from the request and approves with the edited tunnel', async () => {
    const aliceDevice = device({ id: 'dev_alice', userId: 'usr_alice', username: 'alice', name: 'alice-pc' })
    const calls = mockApi({
      ...adminRoutes(),
      'GET /devices': { devices: [device(), aliceDevice] },
      'GET /requests': {
        requests: [request({ deviceId: 'dev_alice', deviceName: 'alice-pc', payload: { type: 'https', subdomain: 'demo', localIp: '127.0.0.1', localPort: 5173, durationHours: 24 } })],
        pending: 1,
      },
      'POST /requests/req_1/approve': { tunnelId: 'tun_new' },
    })
    renderAt('/requests')
    const user = userEvent.setup()
    expect(await screen.findByRole('tab', { name: '待处理 1' })).toHaveAttribute('aria-selected', 'true')
    const approve = await screen.findByRole('button', { name: '批准…' })
    await waitFor(() => expect(approve).toBeEnabled())
    const before = Date.now()
    await user.click(approve)

    const form = await screen.findByRole('form', { name: '隧道表单' })
    expect(screen.getByRole('dialog', { name: '批准申请 · alice' })).toBeInTheDocument()
    expect(within(form).getByLabelText('所属用户')).toHaveValue('usr_alice')
    expect(within(form).getByLabelText('所属用户')).toBeDisabled()
    expect(within(form).getByLabelText('绑定设备')).toHaveValue('dev_alice')
    // The device asked for "demo"; the owner's prefix is added.
    expect(within(form).getByTestId('subdomain-prefix')).toHaveTextContent('alice-')
    expect(within(form).getByLabelText('子域名')).toHaveValue('demo')
    expect(within(form).getByLabelText('名称')).toHaveValue('alice-demo')
    expect(within(form).getByLabelText('本地端口')).toHaveValue('5173')
    const exp = fromLocalInput((within(form).getByLabelText('有效期至') as HTMLInputElement).value)!
    expect(Math.abs(exp - (before + 24 * 3_600_000))).toBeLessThan(2 * 60_000)
    expect(within(form).getByText(/给客户演示新版页面/)).toBeInTheDocument()

    // The admin tightens it before approving.
    await user.selectOptions(within(form).getByLabelText('访问策略'), 'password')
    await user.type(within(form).getByLabelText('访问密码'), 'demo-pass')
    await user.type(within(form).getByLabelText('审批备注'), '已加访问密码')
    await user.click(within(form).getByRole('button', { name: '批准并创建' }))

    const post = await found(calls, 'POST', '/requests/req_1/approve')
    const body = post.body as { tunnel: TunnelInput; note: string }
    expect(body.note).toBe('已加访问密码')
    expect(body.tunnel).toMatchObject({
      userId: 'usr_alice',
      deviceId: 'dev_alice',
      name: 'alice-demo',
      type: 'https',
      domainId: 'dom_1',
      subdomain: 'alice-demo',
      localIp: '127.0.0.1',
      localPort: 5173,
      accessPolicy: 'password',
      accessPassword: 'demo-pass',
    })
    expect(Math.abs(body.tunnel.expiresAt! - (before + 24 * 3_600_000))).toBeLessThan(2 * 60_000)
    expect(calls.some((c) => c.method === 'POST' && c.path === '/tunnels')).toBe(false)
  })

  it('rejects with a note', async () => {
    const calls = mockApi({ ...adminRoutes(), 'GET /requests': { requests: [request()], pending: 1 }, 'POST /requests/req_1/reject': { ok: true } })
    renderAt('/requests')
    const user = userEvent.setup()
    await user.click(await screen.findByRole('button', { name: '拒绝' }))
    const dialog = await screen.findByRole('dialog', { name: '拒绝申请 · alice' })
    await user.type(within(dialog).getByLabelText('拒绝理由'), '端口不够')
    await user.click(within(dialog).getByRole('button', { name: '拒绝' }))
    expect((await found(calls, 'POST', '/requests/req_1/reject')).body).toEqual({ note: '端口不够' })
  })
})

describe('notification channels', () => {
  it('keeps the stored secrets when the configuration is not edited', async () => {
    const calls = mockApi({ ...adminRoutes(), 'GET /channels': { channels: [channel()], events: ['request.created', 'device.enrolled', 'auth.bruteforce'] }, 'PUT /channels/ch_1': { channel: channel() } })
    renderAt('/channels')
    const user = userEvent.setup()
    expect(await screen.findByText('新申请、登录爆破')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: '编辑' }))
    const form = await screen.findByRole('form', { name: '通知渠道表单' })
    expect(within(form).queryByLabelText('Webhook 地址')).not.toBeInTheDocument()
    await user.clear(within(form).getByLabelText('名称'))
    await user.type(within(form).getByLabelText('名称'), 'ops-2')
    await user.click(within(form).getByLabelText('新设备'))
    await user.click(within(form).getByRole('button', { name: '保存' }))
    const put = await found(calls, 'PUT', '/channels/ch_1')
    expect(put.body).toEqual({ kind: 'webhook', name: 'ops-2', events: ['request.created', 'device.enrolled', 'auth.bruteforce'], enabled: true })
    expect('config' in (put.body as ChannelInput)).toBe(false)
  })

  it('sends the configuration when it is changed, and for a new Telegram channel', async () => {
    const calls = mockApi({
      ...adminRoutes(),
      'GET /channels': { channels: [channel()], events: ['request.created', 'device.enrolled'] },
      'PUT /channels/ch_1': { channel: channel() },
      'POST /channels': { status: 201, body: { channel: channel({ id: 'ch_2', kind: 'telegram' }) } },
    })
    renderAt('/channels')
    const user = userEvent.setup()
    await user.click(await screen.findByRole('button', { name: '编辑' }))
    let form = await screen.findByRole('form', { name: '通知渠道表单' })
    await user.click(within(form).getByRole('button', { name: '修改配置' }))
    await user.type(within(form).getByLabelText('Webhook 地址'), 'https://new.example.com/hook')
    await user.type(within(form).getByLabelText('签名密钥'), 's3cret')
    await user.click(within(form).getByRole('button', { name: '保存' }))
    expect((await found(calls, 'PUT', '/channels/ch_1')).body).toMatchObject({ config: { url: 'https://new.example.com/hook', secret: 's3cret' } })

    await user.click(await screen.findByRole('button', { name: /添加渠道/ }))
    form = await screen.findByRole('form', { name: '通知渠道表单' })
    await user.selectOptions(within(form).getByLabelText('类型'), 'telegram')
    await user.type(within(form).getByLabelText('名称'), 'tg')
    await user.click(within(form).getByRole('button', { name: '保存' }))
    expect(await within(form).findByRole('alert')).toHaveTextContent('请填写 Bot Token 和 Chat ID')
    await user.type(within(form).getByLabelText('Bot Token'), '123:abc')
    await user.type(within(form).getByLabelText('Chat ID'), '-100')
    await user.click(within(form).getByRole('button', { name: '保存' }))
    expect((await found(calls, 'POST', '/channels')).body).toEqual({
      kind: 'telegram',
      name: 'tg',
      events: ['request.created', 'device.enrolled'],
      enabled: true,
      config: { botToken: '123:abc', chatId: '-100' },
    })
  })

  it('shows why a test message failed', async () => {
    mockApi({
      ...adminRoutes(),
      'GET /channels': { channels: [channel()], events: ['request.created'] },
      'POST /channels/ch_1/test': { status: 502, body: { error: 'delivery_failed', message: 'webhook 返回 500' } },
    })
    renderAt('/channels')
    const user = userEvent.setup()
    await user.click(await screen.findByRole('button', { name: '测试 ops' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('“ops”发送失败：webhook 返回 500')
  })
})

describe('login hand-off (next)', () => {
  it('accepts only same-origin paths', () => {
    expect(safeNext('/tunnel-login?t=tun_1&cb=https%3A%2F%2Fx')).toBe('/tunnel-login?t=tun_1&cb=https%3A%2F%2Fx')
    expect(safeNext('/devices')).toBe('/devices')
    for (const bad of ['https://evil.example', '//evil.example', '/\\evil.example', 'javascript:alert(1)', '/\t/evil.example', '', null, undefined, 'tunnel-login']) {
      expect(safeNext(bad)).toBeNull()
    }
  })

  const gate = '/tunnel-login?t=tun_1&cb=https%3A%2F%2Fblog.example.com%2F__nyatunnel%2Fauth&r=%2F'

  function signInRoutes() {
    let signedIn = false
    return mockApi({
      ...adminRoutes(),
      'GET /bootstrap': () => (signedIn ? boot() : boot({ user: undefined })),
      'POST /auth/login': () => {
        signedIn = true
        return { user: me().user }
      },
    })
  }

  async function signIn() {
    const user = userEvent.setup()
    await user.type(await screen.findByLabelText('用户名'), 'nya')
    await user.type(screen.getByLabelText('密码'), 'correct horse battery')
    await user.click(screen.getByRole('button', { name: '登录' }))
  }

  it('does a full page load to the tunnel login after signing in', async () => {
    const assign = vi.spyOn(browser, 'assign').mockImplementation(() => {})
    signInRoutes()
    renderAt(`/login?next=${encodeURIComponent(gate)}`)
    expect(await screen.findByText(/登录后会自动返回该网站/)).toBeInTheDocument()
    await signIn()
    await waitFor(() => expect(assign).toHaveBeenCalledWith(gate))
    expect(assign).toHaveBeenCalledTimes(1)
  })

  it('goes on at once when already signed in', async () => {
    const assign = vi.spyOn(browser, 'assign').mockImplementation(() => {})
    mockApi(adminRoutes())
    renderAt(`/login?next=${encodeURIComponent(gate)}`)
    await waitFor(() => expect(assign).toHaveBeenCalledWith(gate))
  })

  it('ignores an unsafe next and lands in the console', async () => {
    const assign = vi.spyOn(browser, 'assign').mockImplementation(() => {})
    signInRoutes()
    renderAt(`/login?next=${encodeURIComponent('//evil.example/x')}`)
    await signIn()
    expect(await screen.findByRole('heading', { name: '仪表盘' })).toBeInTheDocument()
    expect(assign).not.toHaveBeenCalled()
  })

  it('ignores an absolute URL, and follows an in-app path inside the app', async () => {
    const assign = vi.spyOn(browser, 'assign').mockImplementation(() => {})
    mockApi(adminRoutes())
    const { router } = renderAt(`/login?next=${encodeURIComponent('https://evil.example/')}`)
    expect(await screen.findByRole('heading', { name: '仪表盘' })).toBeInTheDocument()
    expect(assign).not.toHaveBeenCalled()
    await router.navigate({ to: '/login', search: { next: '/devices' } as never })
    expect(await screen.findByRole('heading', { name: '设备' })).toBeInTheDocument()
    expect(assign).not.toHaveBeenCalled()
  })
})

describe('user quota', () => {
  it('sends the whole quota with converted units', async () => {
    const calls = mockApi({
      ...adminRoutes(),
      'GET /users': { users: [listed(normalUser(), { monthBytes: 2 * 1024 ** 3, quota: quota({ monthlyTrafficMb: 0 }) })] },
      'PUT /users/usr_alice/quota': { ok: true },
    })
    renderAt('/users')
    const user = userEvent.setup()
    expect(await screen.findByText('关闭（全部审批）')).toBeInTheDocument()
    expect(screen.getByText('2 GB')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'alice 的额度' }))
    const form = await screen.findByRole('form', { name: '自助额度' })
    expect(within(form).getByLabelText('最多隧道数')).toBeDisabled()
    await user.click(within(form).getByLabelText('允许自助创建隧道'))
    await user.click(within(form).getByRole('button', { name: '保存' }))
    expect(await within(form).findByRole('alert')).toHaveTextContent('至少允许一种类型')

    await user.type(within(form).getByLabelText('最多隧道数'), '3')
    await user.click(within(form).getByLabelText('HTTPS'))
    await user.type(within(form).getByLabelText('单隧道带宽'), '10')
    await user.type(within(form).getByLabelText('最长有效期'), '7')
    await user.click(within(form).getByLabelText(/强制显示首次访问提示页/))
    await user.type(within(form).getByLabelText('账号月流量'), '50')
    await user.click(within(form).getByRole('button', { name: '保存' }))
    const put = await found(calls, 'PUT', '/users/usr_alice/quota')
    expect(put.body as Quota).toEqual({
      enabled: true,
      maxTunnels: 3,
      types: ['https'],
      maxBandwidthKbps: 10000,
      maxDays: 7,
      interstitial: true,
      monthlyTrafficMb: 51200,
    })
  })
})

describe('custom domains', () => {
  it('lets a user ask for a domain and shows the DNS instruction', async () => {
    const calls = mockApi({
      ...userRoutes(),
      'GET /domains': {
        domains: [domain(), domain({ id: 'dom_c', name: 'shop.alice.me', kind: 'custom', ownerUserId: 'usr_alice', status: 'dns', checkError: 'shop.alice.me 解析到 1.2.3.4，应指向 203.0.113.10' })],
        publicIps: ['203.0.113.10'],
      },
      'POST /domains/custom': { status: 201, body: { domain: domain({ kind: 'custom', status: 'pending' }) } },
      'POST /domains/dom_c/check': { domain: domain({ id: 'dom_c', name: 'shop.alice.me', kind: 'custom', status: 'active' }) },
    })
    renderAt('/domains')
    const user = userEvent.setup()
    expect(await screen.findByRole('heading', { name: '我的自定义域名' })).toBeInTheDocument()
    expect(screen.queryByRole('heading', { name: '隧道根域名' })).not.toBeInTheDocument()
    expect(await screen.findByText('等待 DNS')).toBeInTheDocument()
    await user.type(screen.getByRole('textbox', { name: '自定义域名' }), 'blog.alice.me')
    expect(screen.getByTestId('dns-instruction')).toHaveTextContent('将 blog.alice.me 的 A/AAAA 记录指向 203.0.113.10（Cloudflare 上关闭代理）')
    await user.click(screen.getByRole('button', { name: /申请自定义域名/ }))
    expect((await found(calls, 'POST', '/domains/custom')).body).toEqual({ name: 'blog.alice.me' })
    await user.click(screen.getByRole('button', { name: '重新检查 shop.alice.me' }))
    expect(await screen.findByText('shop.alice.me 已生效。')).toBeInTheDocument()
  })

  it('lets an admin approve a pending custom domain', async () => {
    const calls = mockApi({
      ...adminRoutes(),
      'GET /domains': { domains: [domain(), domain({ id: 'dom_p', name: 'blog.alice.me', kind: 'custom', ownerUserId: 'usr_alice', ownerName: 'alice', status: 'pending' })], publicIps: ['203.0.113.10'] },
      'PATCH /domains/dom_p': { ok: true },
    })
    renderAt('/domains')
    const user = userEvent.setup()
    await user.click(await screen.findByRole('button', { name: '批准' }))
    expect((await found(calls, 'PATCH', '/domains/dom_p')).body).toEqual({ action: 'approve' })
  })
})

describe('audit labels', () => {
  it('names the new actions and highlights visitor abuse reports', async () => {
    const at = Date.now() - 60_000
    const ev = (id: number, action: string) => ({ id, at, actorType: 'anonymous', actorId: '', actorName: '', action, target: 'tun_1', detail: '', ip: '198.51.100.7' })
    mockApi({ ...adminRoutes(), 'GET /audit': { events: [ev(3, 'tunnel.reported'), ev(2, 'request.approve'), ev(1, 'auth.recovery_codes_regenerated')] } })
    renderAt('/audit')
    const report = await screen.findByText('访客举报')
    expect(report).toHaveClass('tag', 'bad')
    expect(report.closest('tr')).toHaveClass('denied')
    expect(screen.getByText('批准申请').closest('tr')).not.toHaveClass('denied')
    expect(screen.getByText('重新生成恢复码')).toBeInTheDocument()
  })

  it('labels the abuse report notification event', async () => {
    mockApi({ ...adminRoutes(), 'GET /channels': { channels: [], events: ['request.created', 'abuse.report'] } })
    renderAt('/channels')
    const user = userEvent.setup()
    await user.click(await screen.findByRole('button', { name: /添加渠道/ }))
    const form = await screen.findByRole('form', { name: '通知渠道表单' })
    expect(within(form).getByLabelText('举报')).toBeChecked()
  })
})

describe('traffic chart', () => {
  it('fills missing hours and folds long ranges into days', () => {
    const now = Date.UTC(2026, 9, 4, 12, 30)
    const hour = 3_600_000
    const h12 = Date.UTC(2026, 9, 4, 12)
    const series = [
      { hour: h12, bytesIn: 10, bytesOut: 5, conns: 1 },
      { hour: h12 - 3 * hour, bytesIn: 1, bytesOut: 1, conns: 1 },
      { hour: h12 - 30 * hour, bytesIn: 100, bytesOut: 0, conns: 1 },
    ]
    const day = bucketize(series, 24, now)
    expect(day).toHaveLength(24)
    expect(day[23]).toMatchObject({ start: h12, bytesIn: 10, bytesOut: 5 })
    expect(day[20]).toMatchObject({ bytesIn: 1 })
    expect(day.reduce((n, b) => n + b.bytesIn, 0)).toBe(11)
    const week = bucketize(series, 168, now)
    expect(week).toHaveLength(7)
    expect(week[6].bytesIn).toBe(11)
    expect(week[5].bytesIn).toBe(100)
    // Days are local calendar days: the last bucket is today, starting at local midnight.
    const midnight = new Date(now)
    midnight.setHours(0, 0, 0, 0)
    expect(week[6].start).toBe(midnight.getTime())
  })
})
