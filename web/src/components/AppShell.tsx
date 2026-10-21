import ApiOutlined from '@ant-design/icons/ApiOutlined'
import BarChartOutlined from '@ant-design/icons/BarChartOutlined'
import BugOutlined from '@ant-design/icons/BugOutlined'
import ExperimentOutlined from '@ant-design/icons/ExperimentOutlined'
import MenuOutlined from '@ant-design/icons/MenuOutlined'
import MoonOutlined from '@ant-design/icons/MoonOutlined'
import ReloadOutlined from '@ant-design/icons/ReloadOutlined'
import SunOutlined from '@ant-design/icons/SunOutlined'
import { useIsFetching, useQuery, useQueryClient } from '@tanstack/react-query'
import { Badge, Button, Drawer, Grid, Layout, Menu, Segmented, Space, Tooltip, Typography } from 'antd'
import type { MenuProps } from 'antd'
import { useMemo, useState } from 'react'
import { Outlet, useLocation, useNavigate } from 'react-router-dom'
import { getHealth } from '@/api/client'
import { useI18n } from '@/i18n/I18nContext'
import { useTheme } from '@/theme/ThemeContext'

const { Sider, Header, Content } = Layout

export function AppShell() {
  const { t, locale, setLocale } = useI18n()
  const { mode, toggleTheme } = useTheme()
  const navigate = useNavigate()
  const location = useLocation()
  const screens = Grid.useBreakpoint()
  const queryClient = useQueryClient()
  const isFetching = useIsFetching()
  const [collapsed, setCollapsed] = useState(false)
  const [mobileNavOpen, setMobileNavOpen] = useState(false)
  const compactNavigation = !screens.lg
  const health = useQuery({
    queryKey: ['health'],
    queryFn: ({ signal }) => getHealth(signal),
    refetchInterval: 15000,
    retry: false,
  })

  const selectedKey = location.pathname.startsWith('/runs')
    ? '/runs'
    : location.pathname.startsWith('/sweeps')
      ? '/sweeps'
      : '/'

  const items = useMemo<MenuProps['items']>(
    () => [
      { key: '/', icon: <BarChartOutlined />, label: t('nav.overview') },
      { key: '/runs', icon: <BugOutlined />, label: t('nav.runs') },
      { key: '/sweeps', icon: <ExperimentOutlined />, label: t('nav.sweeps') },
    ],
    [t],
  )

  const healthy = health.isSuccess && health.data?.status === 'ok'
  const healthLabel = health.isLoading
    ? t('common.loading')
    : healthy
      ? t('common.connected')
      : t('common.offline')

  const navigateTo = (key: string) => {
    setMobileNavOpen(false)
    navigate(key)
  }

  return (
    <Layout className="app-layout">
      <a className="skip-link" href="#main-content">
        {t('nav.skip')}
      </a>
      {!compactNavigation ? (
        <Sider
          className="app-sider"
          width={224}
          collapsedWidth={64}
          collapsible
          collapsed={collapsed}
          onCollapse={setCollapsed}
        >
          <Brand collapsed={collapsed} title={t('app.title')} subtitle={t('app.subtitle')} />
          <Menu
            mode="inline"
            theme={mode}
            selectedKeys={[selectedKey]}
            items={items}
            onClick={({ key }) => navigateTo(key)}
          />
        </Sider>
      ) : null}
      <Layout>
        <Header className="app-header">
          <div className="header-leading">
            {compactNavigation ? (
              <>
                <Button
                  type="text"
                  className="mobile-nav-trigger"
                  icon={<MenuOutlined />}
                  aria-label={t('nav.open')}
                  aria-expanded={mobileNavOpen}
                  onClick={() => setMobileNavOpen(true)}
                />
                <div className="mobile-brand">
                  <span className="brand-mark">RH</span>
                  <Typography.Text strong>{t('app.title')}</Typography.Text>
                </div>
              </>
            ) : (
              <Space size="middle" wrap>
                <Tooltip title={healthLabel}>
                  <Badge
                    status={healthy ? 'success' : health.isLoading ? 'processing' : 'error'}
                    text={<span className="health-label">{healthLabel}</span>}
                  />
                </Tooltip>
                <Button
                  icon={<ApiOutlined />}
                  href="/healthz"
                  target="_blank"
                  size="small"
                  className="header-secondary"
                >
                  healthz
                </Button>
              </Space>
            )}
          </div>
          <Space size="small" wrap className="header-actions">
            {compactNavigation ? (
              <Tooltip title={healthLabel}>
                <Badge
                  className="mobile-health"
                  status={healthy ? 'success' : health.isLoading ? 'processing' : 'error'}
                />
              </Tooltip>
            ) : null}
            <Segmented
              size="small"
              value={locale}
              options={[
                { label: t('locale.zh'), value: 'zh' },
                { label: t('locale.en'), value: 'en' },
              ]}
              onChange={(value) => setLocale(value as 'zh' | 'en')}
            />
            <Tooltip title={t('theme.toggle')}>
              <Button
                type="text"
                icon={mode === 'dark' ? <SunOutlined /> : <MoonOutlined />}
                onClick={toggleTheme}
                aria-label={t('theme.toggle')}
              />
            </Tooltip>
            <Tooltip title={t('common.refresh')}>
              <Button
                type="text"
                icon={<ReloadOutlined spin={isFetching > 0} />}
                onClick={() => {
                  void queryClient.invalidateQueries()
                }}
                aria-label={t('common.refresh')}
              />
            </Tooltip>
          </Space>
        </Header>
        <Content id="main-content" className="app-content" tabIndex={-1}>
          <Outlet />
        </Content>
      </Layout>
      <Drawer
        className="mobile-nav-drawer"
        placement="left"
        width={288}
        open={compactNavigation && mobileNavOpen}
        onClose={() => setMobileNavOpen(false)}
        closable={false}
        aria-label={t('nav.open')}
      >
        <div className="mobile-nav-panel">
          <Brand
            collapsed={false}
            title={t('app.title')}
            subtitle={t('app.subtitle')}
            closeLabel={t('nav.close')}
            onClose={() => setMobileNavOpen(false)}
          />
          <Menu
            mode="inline"
            theme={mode}
            selectedKeys={[selectedKey]}
            items={items}
            onClick={({ key }) => navigateTo(key)}
          />
        </div>
      </Drawer>
    </Layout>
  )
}

function Brand({
  collapsed,
  title,
  subtitle,
  closeLabel,
  onClose,
}: {
  collapsed: boolean
  title: string
  subtitle: string
  closeLabel?: string
  onClose?: () => void
}) {
  return (
    <div className="brand">
      <div className="brand-mark">RH</div>
      {!collapsed ? (
        <div className="brand-copy">
          <Typography.Text strong>{title}</Typography.Text>
          <span>{subtitle}</span>
        </div>
      ) : null}
      {onClose ? (
        <Button
          type="text"
          className="mobile-nav-close"
          aria-label={closeLabel}
          onClick={onClose}
        >
          ×
        </Button>
      ) : null}
    </div>
  )
}
