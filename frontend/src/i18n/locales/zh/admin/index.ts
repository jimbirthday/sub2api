import overview from './overview'
import channels from './channels'
import accounts from './accounts'
import resources from './resources'
import ops from './ops'
import settings from './settings'
import audit from './audit'
import promptAudit from './promptAudit'
import plugins from './plugins'
import promptInjection from './promptInjection'
import modelBans from './modelBans'
import distillationRisk from './distillationRisk'

export default {
  ...overview,
  ...channels,
  ...accounts,
  ...resources,
  ...ops,
  ...settings,
  ...audit,
  ...promptAudit,
  ...plugins,
  ...promptInjection,
  ...modelBans,
  ...distillationRisk,
}
