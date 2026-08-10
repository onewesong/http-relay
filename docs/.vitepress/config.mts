import { defineConfig } from 'vitepress'

export default defineConfig({
  lang: 'zh-CN',
  title: 'http-relay',
  description: '轻量、可观察、可编程的 HTTP 转发工具',
  base: '/http-relay/',
  lastUpdated: true,
  srcExclude: ['plans/**', 'configuration.zh-CN.md'],
  sitemap: {
    hostname: 'https://onewesong.github.io/http-relay/'
  },
  head: [
    ['meta', { name: 'theme-color', content: '#0f766e' }],
    ['link', { rel: 'icon', href: '/http-relay/logo.svg', type: 'image/svg+xml' }]
  ],
  themeConfig: {
    logo: '/logo.svg',
    siteTitle: 'http-relay',
    nav: [
      { text: '指南', link: '/guide/getting-started' },
      { text: '核心功能', link: '/features/proxy-modes' },
      { text: '脚本改写', link: '/scripting/getting-started' },
      { text: '部署', link: '/deployment/docker' },
      { text: '参考', link: '/reference/cli' }
    ],
    sidebar: [
      {
        text: '开始使用',
        items: [
          { text: '项目介绍', link: '/' },
          { text: '安装与快速开始', link: '/guide/getting-started' },
          { text: '安全说明', link: '/guide/security' }
        ]
      },
      {
        text: '核心功能',
        items: [
          { text: '转发模式与路由', link: '/features/proxy-modes' },
          { text: '流量观察', link: '/features/observability' },
          { text: 'Web UI 与认证', link: '/features/web-ui' }
        ]
      },
      {
        text: 'JavaScript Rewrite',
        items: [
          { text: '编写第一个脚本', link: '/scripting/getting-started' },
          { text: 'Rewrite Profile', link: '/scripting/profiles' },
          { text: '内置兼容脚本', link: '/scripting/builtins' },
          { text: '外部 HTTP API', link: '/scripting/external-http' }
        ]
      },
      {
        text: '部署',
        items: [
          { text: 'Docker', link: '/deployment/docker' },
          { text: '反向代理', link: '/deployment/reverse-proxy' }
        ]
      },
      {
        text: '参考',
        items: [
          { text: '命令行参数', link: '/reference/cli' },
          { text: '配置文件', link: '/reference/configuration' }
        ]
      }
    ],
    socialLinks: [
      { icon: 'github', link: 'https://github.com/onewesong/http-relay' }
    ],
    editLink: {
      pattern: 'https://github.com/onewesong/http-relay/edit/main/docs/:path',
      text: '在 GitHub 上编辑此页'
    },
    search: {
      provider: 'local',
      options: {
        translations: {
          button: { buttonText: '搜索文档', buttonAriaLabel: '搜索文档' },
          modal: {
            noResultsText: '没有找到相关结果',
            resetButtonTitle: '清除查询',
            footer: {
              selectText: '选择',
              navigateText: '切换',
              closeText: '关闭'
            }
          }
        }
      }
    },
    outline: { level: [2, 3], label: '页面导航' },
    lastUpdated: { text: '最后更新于' },
    docFooter: { prev: '上一篇', next: '下一篇' },
    darkModeSwitchLabel: '主题',
    lightModeSwitchTitle: '切换到浅色模式',
    darkModeSwitchTitle: '切换到深色模式',
    returnToTopLabel: '返回顶部',
    sidebarMenuLabel: '菜单'
  }
})
