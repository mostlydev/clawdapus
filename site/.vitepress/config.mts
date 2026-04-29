import { defineConfig } from 'vitepress'

export default defineConfig({
  title: 'Clawdapus',
  description: 'Infrastructure-layer governance for AI agent containers',
  head: [
    // Favicons
    ['link', { rel: 'icon', type: 'image/x-icon', href: '/favicon.ico' }],
    ['link', { rel: 'icon', type: 'image/png', sizes: '32x32', href: '/favicon-32x32.png' }],
    ['link', { rel: 'icon', type: 'image/png', sizes: '16x16', href: '/favicon-16x16.png' }],
    ['link', { rel: 'apple-touch-icon', sizes: '180x180', href: '/apple-touch-icon.png' }],
    ['link', { rel: 'manifest', href: '/site.webmanifest' }],

    // Theme
    ['meta', { name: 'theme-color', content: '#E8501C' }],
    ['meta', { name: 'msapplication-TileColor', content: '#E8501C' }],

    // Open Graph
    ['meta', { property: 'og:type', content: 'website' }],
    ['meta', { property: 'og:site_name', content: 'Clawdapus' }],
    ['meta', { property: 'og:title', content: 'Clawdapus — Docker on Rails for Claws' }],
    ['meta', { property: 'og:description', content: 'Infrastructure-layer governance for AI agent containers. The layer below the framework, where deployment meets governance.' }],
    ['meta', { property: 'og:image', content: 'https://clawdapus.dev/og-image.png' }],
    ['meta', { property: 'og:image:width', content: '1200' }],
    ['meta', { property: 'og:image:height', content: '630' }],
    ['meta', { property: 'og:url', content: 'https://clawdapus.dev' }],

    // Twitter / X
    ['meta', { name: 'twitter:card', content: 'summary_large_image' }],
    ['meta', { name: 'twitter:site', content: '@wojinald' }],
    ['meta', { name: 'twitter:creator', content: '@wojinald' }],
    ['meta', { name: 'twitter:title', content: 'Clawdapus — Docker on Rails for Claws' }],
    ['meta', { name: 'twitter:description', content: 'Infrastructure-layer governance for AI agent containers. The layer below the framework, where deployment meets governance.' }],
    ['meta', { name: 'twitter:image', content: 'https://clawdapus.dev/og-image.png' }],

    // Author
    ['meta', { name: 'author', content: 'Wojtek Grabski' }],
  ],

  themeConfig: {
    logo: '/clawdapus-glyph.png',

    nav: [
      { text: 'Guide', link: '/guide/what-is-clawdapus' },
      {
        text: 'v0.14.0',
        items: [
          { text: 'Changelog', link: '/changelog' },
          { text: 'Manifesto', link: '/manifesto' },
        ],
      },
    ],

    sidebar: [
      {
        text: 'Introduction',
        items: [
          { text: 'What is Clawdapus?', link: '/guide/what-is-clawdapus' },
          { text: 'Quickstart', link: '/guide/quickstart' },
        ],
      },
      {
        text: 'Core Concepts',
        items: [
          { text: 'Anatomy of a Claw', link: '/guide/anatomy' },
          { text: 'Clawfile', link: '/guide/clawfile' },
          { text: 'Pod YAML', link: '/guide/pod-yaml' },
          { text: 'cllama Governance Proxy', link: '/guide/cllama' },
          { text: 'Managed Tools', link: '/guide/tools' },
          { text: 'Memory Plane', link: '/guide/memory' },
          { text: 'Surfaces & Skills', link: '/guide/surfaces-and-skills' },
          { text: 'Compilation Principles', link: '/guide/compilation-principles' },
          { text: 'Social Topology', link: '/guide/social-topology' },
        ],
      },
      {
        text: 'Reference',
        items: [
          { text: 'CLI Commands', link: '/guide/cli' },
          { text: 'Driver Support Matrix', link: '/guide/drivers' },
        ],
      },
    ],

    socialLinks: [
      { icon: 'github', link: 'https://github.com/mostlydev/clawdapus' },
    ],

    footer: {
      message: 'Released under the MIT License.',
      copyright: 'Copyright \u00a9 2025-present Wojtek Grabski · <a href="https://x.com/wojinald">@wojinald</a> · <a href="https://github.com/mostlydev">mostlydev</a>',
    },

    search: {
      provider: 'local',
    },
  },
})
