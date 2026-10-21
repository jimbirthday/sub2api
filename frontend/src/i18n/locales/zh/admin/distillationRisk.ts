export default {
  distillationRisk: {
    concurrency_saturation: '并发饱和比例',
    request_rate_saturation: '速率饱和比例',
    prompt_repeat_ratio: '精确重复比例',
    interarrival_cv: '请求间隔变异系数',
    output_input_ratio: '输出输入比例',

    actionReview: '开始复核',
stale: '当前显示上次成功的数据，刷新失败。',telemetryDegraded: '遥测已降级，自动处置必须等待完整证据。',restrictionUntil: '人工限制到期时间',restrictionHint: '人工限制必须明确到期时间，且在未来 24 小时以内。',
    title: '蒸馏行为风控', description: '依据全部 API Key 和分组的行为证据发现异常并进行复核。行为评分用于辅助审核，不代表已确定用户意图。',
    configuration: '配置', mode: '运行模式', pendingReviews: '待审核', activeRestrictions: '生效中的限制', lastProcessed: '最近窗口处理时间',
    evidenceHint: '仅对 UTC 30 分钟完整窗口评分：窗口结束后等待 10 分钟，且至少包含 200 个工作单位及 10 万 token。缺失信号显示为不可用。',
    userID: '用户 ID', allStates: '全部状态', sections: '风险记录分类', review: '审核', evidence: '证据', score: '评分', reviewStatus: '审核状态', reason: '操作原因', windows: '已完成窗口', noWindows: '尚无完整窗口证据。', units: '工作单位', unavailable: '不可用',
    revokeExemption: '撤销豁免', createExemption: '添加豁免', exemptionUntil: '豁免到期时间', exemptionHint: '仅豁免自动处置，请选择未来 30 天以内的时间。人工限制仍然生效。',
    snapshotAt: '数据加载于 {time}', configHint: '观察模式记录证据，不自动处罚。执行模式至少需要 7 天观察期。关闭执行后停止自动限制。', observingSince: '开始观察时间：{time}',
    userDetail: '用户 #{id}', limitSummary: '并发 {concurrency} · RPM {rpm} · 到期 {until}', actionConfirm: '确认对该用户执行此操作？操作及原因将留痕。',
    modeOff: '关闭', modeObserve: '仅观察', modeEnforce: '执行', stateClear: '无当前告警', stateObserving: '观察中', stateSuspected: '待复核', stateLimited: '已限制',
    actionConfirmed: '确认案件', actionDismiss: '驳回案件', actionRestrict: '人工临时限制', actionRevoke: '解除限制',
    signalConcurrency: '并发饱和', signalRate: '请求速率', signalTokens: 'Token 吞吐', signalRepeat: '完全重复比例', signalBreadth: '供应商与模型广度', signalRegularity: '请求间隔规律', signalOutput: '输出放大',
    users: '用户', events: '事件', exemptions: '豁免', updated: '更新时间', eventType: '事件类型', actor: '管理员',
    conflict: '记录已变更，或尚未满足执行条件，请刷新数据后重试。', failed: '暂时无法获取风控数据，请重试。', invalid: '请填写有效用户 ID、原因及未来 30 天以内的到期时间。', reasonRequired: '执行操作前请填写原因。', saved: '风控配置或操作已保存'
  }
}
