const canonicalTranslation = {
  common: {
    locale: {
      label: "语言",
    },
    readState: {
      loading: "加载中",
      unavailable: "暂不可用",
      empty: "暂无记录",
      retry: "重试",
    },
    shell: {
      authLoading: "正在读取安全状态",
      routeLoading: "正在加载页面",
      authUnavailable: "Control 认证服务暂时不可用",
      authUnavailableDescription: "管理面已安全关闭；请检查 Control 与 PostgreSQL 状态。",
    },
  },
  auth: {
    errors: {
      unavailable: "请求暂时无法完成，请稍后再试。",
      rateLimited: "尝试次数过多，请稍后再试。",
      challengeExpired: "验证已过期，请重新登录。",
      lastAdministratorProtected: "不能禁用最后一个可用管理员。",
      selfDisableForbidden: "不能禁用当前登录账号。",
      reauthenticationRequired: "请先完成重新认证。",
      unauthorized: "认证失败，请检查输入后重试。",
      csrfInvalid: "安全凭据已变化，请刷新会话后重试。",
      requestIncomplete: "请求未完成（请求 ID：{{requestId}}）",
    },
    login: {
      title: "管理员登录",
      securityHint: "所有认证失败使用统一提示，不确认账号状态。",
      loginName: "登录名",
      password: "密码",
      continue: "继续",
      activationLink: "使用激活令牌设置新账号",
      mfaRequired: "需要第二步验证",
      challengeValidUntil: "挑战有效至 {{date}}",
      totp: "TOTP 验证码",
      totpMethod: "TOTP",
      recoveryCode: "恢复码",
      verify: "验证并登录",
      backToPassword: "返回密码登录",
      recoveryWarning: "恢复码成功使用后将立即失效；登录后请检查剩余数量。",
      expired: "验证已过期，请重新登录。",
    },
    bootstrap: {
      title: "初始化 Control",
      inProgress: "存在未完成的初始化，请重新输入相同资料继续，或使用运行时 Secret 重置。",
      newInstallation: "创建首个实名超级管理员。",
      secret: "运行时 Bootstrap Secret",
      loginName: "登录名",
      displayName: "实名显示名",
      password: "密码",
      start: "开始或继续初始化",
      resetPending: "重置未完成流程",
      addAuthenticator: "请立即添加到认证器",
      totpMemoryOnly: "此 TOTP 配置仅在当前页面内存中保留。",
      copyUri: "复制 TOTP 配置 URI",
      confirm: "确认并永久完成",
      reset: "重置",
      totpCode: "TOTP 验证码",
    },
    activation: {
      title: "激活管理员账号",
      tokenHint: "请手工粘贴令牌。页面不会从 URL、历史记录或浏览器存储读取令牌。",
      token: "一次性激活令牌",
      start: "开始激活",
      newPassword: "新密码",
      totpCode: "TOTP 验证码",
      complete: "完成激活",
      backToLogin: "返回登录",
      leaveHint: "离开此页后不会保留。",
    },
    oneTime: {
      recoveryTitle: "保存恢复码",
      activationTitle: "复制激活令牌",
      onlyShownOnce: "只显示这一次",
      recoveryDescription: "每个恢复码只能使用一次。请保存到密码管理器或离线安全位置。",
      activationDescription: "令牌只应通过可信渠道交给对应管理员，禁止放入 URL。",
      validUntil: "有效至 {{date}}",
      confirmation: "我已安全保存，理解离开后无法再次查看",
      leave: "确认并离开",
      noCache: "本页设置禁止缓存；离开时材料会从应用内存移除。",
    },
  },
} as const;

type LeafKeys<T> = {
  [Key in keyof T & string]: T[Key] extends Record<string, unknown>
    ? `${Key}.${LeafKeys<T[Key]>}`
    : Key;
}[keyof T & string];

type ResourceShape<T> = {
  [Key in keyof T]: T[Key] extends Record<string, unknown> ? ResourceShape<T[Key]> : string;
};

export type TranslationKey = LeafKeys<typeof canonicalTranslation>;
export type TranslationResource = ResourceShape<typeof canonicalTranslation>;

export const resources = {
  "zh-CN": {
    translation: canonicalTranslation,
  },
  en: {
    translation: {
      common: {
        locale: {
          label: "Language",
        },
        readState: {
          loading: "Loading",
          unavailable: "Unavailable",
          empty: "No records",
          retry: "Retry",
        },
        shell: {
          authLoading: "Reading security state",
          routeLoading: "Loading page",
          authUnavailable: "Control authentication service is temporarily unavailable",
          authUnavailableDescription: "The management surface is safely closed; check Control and PostgreSQL status.",
        },
      },
      auth: {
        errors: {
          unavailable: "The request could not be completed. Try again later.",
          rateLimited: "Too many attempts. Try again later.",
          challengeExpired: "The verification expired. Sign in again.",
          lastAdministratorProtected: "The last available administrator cannot be disabled.",
          selfDisableForbidden: "The current signed-in account cannot be disabled.",
          reauthenticationRequired: "Complete reauthentication first.",
          unauthorized: "Authentication failed. Check the input and try again.",
          csrfInvalid: "The security credential changed. Refresh the session and try again.",
          requestIncomplete: "The request was not completed (request ID: {{requestId}})",
        },
        login: {
          title: "Administrator sign in",
          securityHint: "All authentication failures use one response and do not confirm account state.",
          loginName: "Login name",
          password: "Password",
          continue: "Continue",
          activationLink: "Set up a new account with an activation token",
          mfaRequired: "Second-step verification required",
          challengeValidUntil: "Challenge valid until {{date}}",
          totp: "TOTP code",
          totpMethod: "TOTP",
          recoveryCode: "Recovery code",
          verify: "Verify and sign in",
          backToPassword: "Back to password sign-in",
          recoveryWarning: "A recovery code becomes invalid after use; check the remaining count after sign-in.",
          expired: "The verification expired. Sign in again.",
        },
        bootstrap: {
          title: "Initialize Control",
          inProgress: "An incomplete initialization exists. Enter the same details to continue, or reset it with the runtime Secret.",
          newInstallation: "Create the first named super administrator.",
          secret: "Runtime Bootstrap Secret",
          loginName: "Login name",
          displayName: "Display name",
          password: "Password",
          start: "Start or continue initialization",
          resetPending: "Reset incomplete flow",
          addAuthenticator: "Add this to an authenticator now",
          totpMemoryOnly: "This TOTP configuration remains only in page memory.",
          copyUri: "Copy TOTP configuration URI",
          confirm: "Confirm and complete permanently",
          reset: "Reset",
          totpCode: "TOTP code",
        },
        activation: {
          title: "Activate administrator account",
          tokenHint: "Paste the token manually. The page never reads it from the URL, history, or browser storage.",
          token: "One-time activation token",
          start: "Start activation",
          newPassword: "New password",
          totpCode: "TOTP code",
          complete: "Complete activation",
          backToLogin: "Back to sign-in",
          leaveHint: "It will not be retained after leaving this page.",
        },
        oneTime: {
          recoveryTitle: "Save recovery codes",
          activationTitle: "Copy activation token",
          onlyShownOnce: "Shown only once",
          recoveryDescription: "Each recovery code can be used once. Save them in a password manager or another secure offline location.",
          activationDescription: "Give the token only to the intended administrator through a trusted channel; never put it in a URL.",
          validUntil: "Valid until {{date}}",
          confirmation: "I saved it securely and understand it cannot be viewed again after leaving",
          leave: "Confirm and leave",
          noCache: "This page is configured not to cache; leaving removes the material from application memory.",
        },
      },
    } satisfies TranslationResource,
  },
} satisfies Record<"zh-CN" | "en", { translation: TranslationResource }>;
