import { defineConfig } from 'vitepress'

export default defineConfig({
  title: 'Clawdapus',
  description: 'Infrastructure-layer governance for AI agent containers',

  head: [
    ['link', { rel: 'icon', href: '/clawdapus.png' }],
  ],

  themeConfig: {
    logo: '/clawdapus.png',

    nav: [
      { text: 'Guide', link: '/guide/what-is-clawdapus' },
      { text: 'Reference', link: '/reference/cli' },
      {
        text: 'v0.3.2',
        items: [
          { text: 'Changelog', link: '/changelog' },
          { text: 'Manifesto', link: '/manifesto' },
        ],
      },
    ],

    sidebar: {
      '/guide/': [
        {
          text: 'Introduction',
          items: [
            { text: 'What is Clawdapus', link: '/guide/what-is-clawdapus' },
            { text: 'Quickstart', link: '/guide/quickstart' },
          ],
        },
        {
          text: 'Core Concepts',
          items: [
            { text: 'Anatomy', link: '/guide/anatomy' },
            { text: 'Clawfile', link: '/guide/clawfile' },
            { text: 'Pod YAML', link: '/guide/pod-yaml' },
            { text: 'cllama', link: '/guide/cllama' },
            { text: 'Social Topology', link: '/guide/social-topology' },
          ],
        },
      ],
      '/reference/': [
        {
          text: 'Reference',
          items: [
            { text: 'CLI Commands', link: '/reference/cli' },
            { text: 'Clawfile Directives', link: '/reference/clawfile' },
            { text: 'Driver Support Matrix', link: '/reference/drivers' },
            { text: 'cllama Spec', link: '/reference/cllama-spec' },
          ],
        },
      ],
    },

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
