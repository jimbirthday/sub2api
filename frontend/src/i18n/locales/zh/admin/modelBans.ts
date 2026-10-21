export default {
  modelBans: {
    conflictReview: '已有更新版本，请对比后再重新加载。',
    compareVersions: '对比版本',
    yourDraft: '当前未保存草稿',
    latestSaved: '最新已保存版本',
    reloadLatest: '加载最新版本',
    discardDraftConfirm: '确认放弃当前未保存的编辑并加载最新版本？',

    evaluationContext: '校验上下文（可选）',
source: '来源',allSources: '全部来源',manual: '人工',behavior: '行为风控',
changeReason: '变更原因',reasonRequired: '请填写本次变更原因。',protocol: '上游协议',role: '模型角色',providerContext: '供应商上下文',
    pending: '尚未生效',
    action: '规则动作', denyModel: '禁用模型', capConcurrency: '限制模型并发', capRPM: '限制模型 RPM', limit: '上限（正整数）', starts: '开始时间（留空立即生效）',
    policies: '已有禁用', selectUser: '选择用户', allStates: '全部状态', active: '生效中', disabled: '已停用', expired: '已过期', previous: '上一页', next: '下一页', noPolicies: '没有匹配的模型禁用规则。', evaluate: '策略校验', allowed: '允许', denied: '拒绝', unresolved: '模型身份未解析',
    catalog: '模型身份目录', catalogHint: '登记供应商模型及各传输平台使用的确切别名，目录变更影响全部用户。', models: '模型', displayName: '显示名称', aliases: '传输平台别名', transport: '传输平台', alias: '确切别名',
    title: '用户模型禁用', description: '管理用户在全部分组和 API Key 下的模型访问权限。',
    crossGroup: '禁用策略对该用户的所有 API Key 和分组生效。',
    revision: '策略版本 {revision}', history: '变更历史', add: '添加禁用规则', empty: '该用户尚无模型禁用规则。',
    provider: '供应商命名空间', model: '模型标准标识', expires: '到期时间（本地时间）', permanentHint: '留空表示永久禁用。',
    reason: '原因', enabled: '启用', clear: '清除人工限制', clearConfirm: '确认移除此用户的人工模型限制？行为风控限制及变更历史仍会保留。',
    exactHint: '填写确切的供应商命名空间和模型标识，不支持通配符或计费别名。',
    saved: '模型策略已保存', failed: '无法读取或保存模型策略，请重试。',
    invalid: '请为每条规则填写供应商和确切模型名，移除重复规则及通配符。',
    conflict: '其他管理员已修改此策略，请对比最新版本与草稿，确认重新加载后再保存。',
    ruleCount: '{count} 条规则', viewVersion: '查看版本', more: '加载更多', searchUser: '搜索用户邮箱或名称', manage: '管理禁用', user: '用户', noUsers: '没有匹配的用户。'
  }
}
