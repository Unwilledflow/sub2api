import overview from './overview'
import adaptive from './adaptive'
import channels from './channels'
import accounts from './accounts'
import resources from './resources'
import ops from './ops'
import settings from './settings'
import audit from './audit'
import promptAudit from './promptAudit'
import plugins from './plugins'

export default {
  ...overview,
  ...adaptive,
  ...channels,
  ...accounts,
  ...resources,
  ...ops,
  ...settings,
  ...audit,
  ...promptAudit,
  ...plugins,
  // resources.adaptive / antiStall overwrite the kedaya Adaptive module;
  // re-merge so hybrid Adaptive keys win while resources-only copy is kept.
  adaptive: {
    ...resources.adaptive,
    ...adaptive.adaptive
  },
  antiStall: {
    ...adaptive.antiStall,
    ...resources.antiStall
  },
  adaptiveGroups: {
    title: 'Adaptive 分组',
    description: '配置 Adaptive 父组与跨平台叶子池。'
  },
  oauthAccounts: {
    title: 'OAuth 账号',
    description: '独立管理 OpenAI OAuth 账号，不和主账号列表混在一起。'
  }
}
