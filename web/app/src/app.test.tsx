import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { ApiError, CSRF_HEADER, api, createUser, describeError, getBootstrap } from './api'
import { adminRoutes, boot, me, mockApi, normalUser, userRoutes } from './test/mockApi'
import { renderAt } from './test/render'

describe('api client', () => {
  it('sends the CSRF header on writes but not on reads', async () => {
    const calls = mockApi({ 'GET /bootstrap': boot(), 'POST /users': { status: 201, body: { user: normalUser() } } })
    await getBootstrap()
    await createUser({ username: 'alice', password: 'long enough pw', role: 'user' })
    expect(calls[0].headers[CSRF_HEADER]).toBeUndefined()
    expect(calls[1].method).toBe('POST')
    expect(calls[1].path).toBe('/users')
    expect(calls[1].headers[CSRF_HEADER]).toBe('1')
    expect(calls[1].headers['Content-Type']).toBe('application/json')
  })

  it('sends the CSRF header on PUT, PATCH and DELETE too', async () => {
    const calls = mockApi({ 'DELETE /tunnels/x': { ok: true }, 'PATCH /domains/d': { ok: true } })
    await api('/tunnels/x', { method: 'DELETE' })
    await api('/domains/d', { method: 'PATCH', body: { allowUsers: true } })
    expect(calls.every((c) => c.headers[CSRF_HEADER] === '1')).toBe(true)
  })

  it('turns error bodies into ApiError, preferring the server message and falling back to Chinese text', async () => {
    mockApi({
      'POST /a': { status: 409, body: { error: 'domain_in_use', message: '仍有隧道使用该域名' } },
      'POST /b': { status: 401, body: { error: 'totp_required', message: '' } },
      'POST /c': { status: 400, body: { error: 'weak_password', message: 'password must be at least 10 characters' } },
    })
    const fail = async (p: string) => api(p, { method: 'POST' }).catch((e: unknown) => e)
    const a = await fail('/a')
    expect(a).toBeInstanceOf(ApiError)
    expect((a as ApiError).status).toBe(409)
    expect(describeError(a)).toBe('仍有隧道使用该域名')
    expect(describeError(await fail('/b'))).toBe('请输入两步验证码')
    expect(describeError(await fail('/c'))).toBe('密码至少需要 10 个字符')
  })
})

describe('login gate', () => {
  it('shows the setup screen with the setup-token explanation on a fresh server', async () => {
    const calls = mockApi({
      'GET /bootstrap': () => boot({ needsSetup: true, user: undefined }),
      'POST /setup': { user: normalUser() },
    })
    renderAt('/')
    const user = userEvent.setup()
    expect(await screen.findByRole('heading', { name: '初始化服务器' })).toBeInTheDocument()
    expect(screen.getByText(/初始化令牌/, { selector: 'b' })).toBeInTheDocument()

    await user.type(screen.getByLabelText('初始化令牌'), ' tok123 ')
    await user.type(screen.getByLabelText(/^密码/), 'short')
    await user.type(screen.getByLabelText('确认密码'), 'short')
    await user.click(screen.getByRole('button', { name: '创建管理员并登录' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('至少需要 10 个字符')
    expect(calls.some((c) => c.method === 'POST')).toBe(false)

    await user.clear(screen.getByLabelText(/^密码/))
    await user.type(screen.getByLabelText(/^密码/), 'long enough pw')
    await user.clear(screen.getByLabelText('确认密码'))
    await user.type(screen.getByLabelText('确认密码'), 'long enough pw')
    await user.click(screen.getByRole('button', { name: '创建管理员并登录' }))
    await waitFor(() => expect(calls.find((c) => c.method === 'POST')).toBeTruthy())
    const post = calls.find((c) => c.method === 'POST')!
    expect(post.path).toBe('/setup')
    expect(post.body).toEqual({ token: 'tok123', username: 'admin', password: 'long enough pw' })
    expect(post.headers[CSRF_HEADER]).toBe('1')
  })

  it('asks for the TOTP code after totp_required, then signs in with it', async () => {
    let signedIn = false
    const calls = mockApi({
      ...adminRoutes(),
      'GET /bootstrap': () => (signedIn ? boot() : boot({ user: undefined })),
      'POST /auth/login': (req) => {
        const body = req.body as { totp?: string }
        if (!body.totp) return { status: 401, body: { error: 'totp_required', message: '' } }
        if (body.totp !== '123456') return { status: 401, body: { error: 'invalid_totp', message: '验证码不正确' } }
        signedIn = true
        return { user: me().user }
      },
    })
    renderAt('/')
    const user = userEvent.setup()

    await user.type(await screen.findByLabelText('用户名'), 'Nya')
    await user.type(screen.getByLabelText('密码'), 'correct horse battery')
    await user.click(screen.getByRole('button', { name: '登录' }))

    const codeField = await screen.findByLabelText(/两步验证码/)
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    await user.type(codeField, '000000')
    await user.click(screen.getByRole('button', { name: '验证并登录' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('验证码不正确')

    await user.clear(codeField)
    await user.type(codeField, '123456')
    await user.click(screen.getByRole('button', { name: '验证并登录' }))

    // Lands on the admin dashboard.
    expect(await screen.findByRole('heading', { name: '仪表盘' })).toBeInTheDocument()
    const logins = calls.filter((c) => c.path === '/auth/login')
    expect(logins.map((c) => c.body)).toEqual([
      { username: 'nya', password: 'correct horse battery' },
      { username: 'nya', password: 'correct horse battery', totp: '000000' },
      { username: 'nya', password: 'correct horse battery', totp: '123456' },
    ])
    expect(logins.every((c) => c.headers[CSRF_HEADER] === '1')).toBe(true)
  })

  it('can use a recovery code instead of the TOTP code', async () => {
    const calls = mockApi({
      'GET /bootstrap': boot({ user: undefined }),
      'POST /auth/login': (req) =>
        (req.body as { recoveryCode?: string }).recoveryCode
          ? { status: 401, body: { error: 'invalid_recovery_code', message: '' } }
          : { status: 401, body: { error: 'totp_required', message: '' } },
    })
    renderAt('/')
    const user = userEvent.setup()
    await user.type(await screen.findByLabelText('用户名'), 'nya')
    await user.type(screen.getByLabelText('密码'), 'correct horse battery')
    await user.click(screen.getByRole('button', { name: '登录' }))
    await user.click(await screen.findByRole('button', { name: /使用恢复码/ }))
    await user.type(screen.getByLabelText(/^恢复码/), 'abcd-efgh')
    await user.click(screen.getByRole('button', { name: '验证并登录' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('恢复码无效')
    expect(calls.at(-1)!.body).toEqual({ username: 'nya', password: 'correct horse battery', recoveryCode: 'abcd-efgh' })
  })

  it('reports wrong credentials in Chinese', async () => {
    mockApi({
      'GET /bootstrap': boot({ user: undefined }),
      'POST /auth/login': { status: 401, body: { error: 'invalid_credentials', message: '用户名或密码错误' } },
    })
    renderAt('/')
    const user = userEvent.setup()
    await user.type(await screen.findByLabelText('用户名'), 'nya')
    await user.type(screen.getByLabelText('密码'), 'wrong password')
    await user.click(screen.getByRole('button', { name: '登录' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('用户名或密码错误')
  })
})

describe('console shell', () => {
  it('shows the admin navigation and the server name', async () => {
    mockApi(adminRoutes())
    renderAt('/')
    const nav = await screen.findByRole('complementary', { name: '主导航' })
    for (const label of ['仪表盘', '用户', '隧道', '设备', '申请审批', '域名', '端口池', '审计日志', '通知渠道', '系统设置', '账号安全']) {
      expect(within(nav).getByRole('link', { name: label })).toBeInTheDocument()
    }
    expect(screen.getByText('Nya 的家庭网络')).toBeInTheDocument()
  })

  it('shows the number of pending requests next to 申请审批', async () => {
    mockApi({ ...adminRoutes(), 'GET /requests': { requests: [], pending: 3 } })
    renderAt('/')
    const nav = await screen.findByRole('complementary', { name: '主导航' })
    expect(await within(nav).findByLabelText('3 条待处理')).toHaveTextContent('3')
  })

  it('gives normal users their own pages and keeps admin pages closed', async () => {
    mockApi(userRoutes())
    const { router } = renderAt('/')
    expect(await screen.findByRole('heading', { name: '我的隧道' })).toBeInTheDocument()
    const nav = screen.getByRole('complementary', { name: '主导航' })
    expect(within(nav).getAllByRole('link').map((a) => a.textContent)).toEqual(['我的隧道', '我的设备', '我的申请', '自定义域名', '账号安全'])
    // Self-service: users may create and edit their own tunnels (the server checks the quota).
    expect(screen.getByRole('button', { name: /新建隧道/ })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /申请隧道/ })).toBeInTheDocument()
    await router.navigate({ to: '/users' })
    expect(await screen.findByText(/该页面仅管理员可访问/)).toBeInTheDocument()
  })

  it('only offers the security page while TOTP setup is mandatory', async () => {
    const calls = mockApi({
      ...userRoutes(),
      'GET /bootstrap': boot({ user: normalUser(), forceTotp: true }),
      'GET /me': me(normalUser(), true),
      'POST /me/totp/setup': { secret: 'JBSWY3DPEHPK3PXP', otpauthUrl: 'otpauth://totp/NyaTunnel:alice?secret=JBSWY3DPEHPK3PXP&issuer=NyaTunnel' },
    })
    renderAt('/tunnels')
    const user = userEvent.setup()
    expect(await screen.findByText(/完成下面的设置后才能使用其他功能/)).toBeInTheDocument()
    const nav = screen.getByRole('complementary', { name: '主导航' })
    expect(within(nav).getAllByRole('link').map((a) => a.textContent)).toEqual(['账号安全'])
    expect(screen.queryByRole('heading', { name: '我的隧道' })).not.toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: '开始设置' }))
    expect(await screen.findByRole('img', { name: '两步验证二维码' })).toBeInTheDocument()
    expect(screen.getByText('JBSW Y3DP EHPK 3PXP')).toBeInTheDocument()
    expect(calls.some((c) => c.path === '/tunnels')).toBe(false)
  })

  it('shows the recovery codes once and only then lets a forced user into the console', async () => {
    let enabled = false
    const calls = mockApi({
      ...userRoutes(),
      'GET /bootstrap': boot({ user: normalUser(), forceTotp: true }),
      'GET /me': () => (enabled ? me(normalUser({ totpEnabled: true, recoveryCodesLeft: 2 })) : me(normalUser(), true)),
      'POST /me/totp/setup': { secret: 'JBSWY3DPEHPK3PXP', otpauthUrl: 'otpauth://totp/x' },
      'POST /me/totp/enable': () => {
        enabled = true
        return { recoveryCodes: ['aaaa-bbbb', 'cccc-dddd'] }
      },
    })
    renderAt('/tunnels')
    const user = userEvent.setup()
    await user.click(await screen.findByRole('button', { name: '开始设置' }))
    await user.type(await screen.findByLabelText('6 位验证码'), '123456')
    await user.click(screen.getByRole('button', { name: '启用' }))

    const codes = await screen.findByRole('list', { name: '恢复码' })
    expect(within(codes).getAllByRole('listitem').map((li) => li.textContent)).toEqual(['aaaa-bbbb', 'cccc-dddd'])
    expect(calls.find((c) => c.path === '/me/totp/enable')!.body).toEqual({ secret: 'JBSWY3DPEHPK3PXP', code: '123456' })
    // Still the restricted shell until the codes are acknowledged.
    expect(screen.queryByRole('heading', { name: '我的隧道' })).not.toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: '我已保存' }))
    expect(await screen.findByRole('heading', { name: '我的隧道' })).toBeInTheDocument()
  })
})
