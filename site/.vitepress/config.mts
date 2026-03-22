import { defineConfig } from 'vitepress'

export default defineConfig({
  title: 'Clawdapus',
  description: 'Infrastructure-layer governance for AI agent containers',
  base: '/clawdapus/',

  head: [
    ['link', { rel: 'icon', href: '/clawdapus/clawdapus-glyph.png' }],
  ],

  themeConfig: {
    logo: '/clawdapus-glyph.png',

    nav: [
      { text: 'Guide', link: '/guide/what-is-clawdapus' },
      {
        text: 'v0.3.2',
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
      copyright: 'Copyright \u00a9 2025-present Mostly Dev',
    },

    search: {
      provider: 'local',
    },
  },
})
