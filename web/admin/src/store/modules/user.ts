import { defineStore } from "pinia";
import {
  type userType,
  store,
  router,
  resetRouter,
  routerArrays,
  storageLocal
} from "../utils";
import {
  adminLogin,
  adminLogout,
  adminSession,
  adminSetup,
  type AdminSession
} from "@/api/localshare";
import { useMultiTagsStoreHook } from "./multiTags";
import { type DataInfo, setToken, removeToken, userKey } from "@/utils/auth";

const sessionExpires = () => new Date(Date.now() + 7 * 24 * 60 * 60 * 1000);

export const useUserStore = defineStore("pure-user", {
  state: (): userType & { setupRequired: boolean } => ({
    // 头像
    avatar: storageLocal().getItem<DataInfo<number>>(userKey)?.avatar ?? "",
    // 用户名
    username: storageLocal().getItem<DataInfo<number>>(userKey)?.username ?? "",
    // 昵称
    nickname: storageLocal().getItem<DataInfo<number>>(userKey)?.nickname ?? "",
    // 页面级别权限
    roles: storageLocal().getItem<DataInfo<number>>(userKey)?.roles ?? [],
    // 按钮级别权限
    permissions:
      storageLocal().getItem<DataInfo<number>>(userKey)?.permissions ?? [],
    // 前端生成的验证码（保留字段，兼容 pure-admin 类型）
    verifyCode: "",
    // 判断登录页面显示哪个组件（保留字段，兼容 pure-admin 类型）
    currentPage: 0,
    // 是否记住本地 UI 登录状态；真正认证仍由 HttpOnly cookie 决定
    isRemembered: true,
    loginDay: 7,
    setupRequired: false
  }),
  actions: {
    /** 存储头像 */
    SET_AVATAR(avatar: string) {
      this.avatar = avatar;
    },
    /** 存储用户名 */
    SET_USERNAME(username: string) {
      this.username = username;
    },
    /** 存储昵称 */
    SET_NICKNAME(nickname: string) {
      this.nickname = nickname;
    },
    /** 存储角色 */
    SET_ROLES(roles: Array<string>) {
      this.roles = roles;
    },
    /** 存储按钮级别权限 */
    SET_PERMS(permissions: Array<string>) {
      this.permissions = permissions;
    },
    /** 存储前端生成的验证码 */
    SET_VERIFYCODE(verifyCode: string) {
      this.verifyCode = verifyCode;
    },
    /** 存储登录页面显示哪个组件 */
    SET_CURRENTPAGE(value: number) {
      this.currentPage = value;
    },
    /** 存储是否勾选了登录页的免登录 */
    SET_ISREMEMBERED(bool: boolean) {
      this.isRemembered = bool;
    },
    /** 设置登录页的免登录存储几天 */
    SET_LOGINDAY(value: number) {
      this.loginDay = Number(value);
    },
    SET_SETUP_REQUIRED(value: boolean) {
      this.setupRequired = value;
    },
    applySession() {
      this.SET_ISREMEMBERED(true);
      this.SET_LOGINDAY(7);
      setToken({
        accessToken: "localshare-session",
        refreshToken: "localshare-session",
        expires: sessionExpires(),
        avatar: "",
        username: "admin",
        nickname: "Localshare Admin",
        roles: ["admin"],
        permissions: ["*:*:*"]
      });
    },
    clearSession() {
      this.username = "";
      this.nickname = "";
      this.roles = [];
      this.permissions = [];
      removeToken();
    },
    /** 登入，兼容 pure-admin 调用名 */
    async loginByUsername(data: { password: string }) {
      await adminLogin(data.password);
      this.applySession();
      return {
        code: 0,
        message: "ok",
        data: storageLocal().getItem<DataInfo<number>>(userKey)
      };
    },
    async setupPassword(password: string) {
      await adminSetup(password);
      this.SET_SETUP_REQUIRED(false);
    },
    async ensureSession(): Promise<AdminSession> {
      try {
        const session = await adminSession();
        this.SET_SETUP_REQUIRED(session.setup_required);
        if (session.authenticated) {
          this.applySession();
        } else {
          this.clearSession();
        }
        return session;
      } catch {
        this.clearSession();
        return { authenticated: false, setup_required: false };
      }
    },
    /** 登出 */
    async logOut() {
      try {
        await adminLogout();
      } catch {
        // 本地状态必须清掉，服务端 session 清理失败由下次请求兜底。
      }
      this.clearSession();
      useMultiTagsStoreHook().handleTags("equal", [...routerArrays]);
      resetRouter();
      router.push("/login");
    },
    /** 保留方法名，避免旧演示页编译时引用失效 */
    async handRefreshToken() {
      return {
        code: 0,
        message: "ok",
        data: {
          accessToken: "localshare-session",
          refreshToken: "localshare-session",
          expires: sessionExpires()
        }
      };
    }
  }
});

export function useUserStoreHook() {
  return useUserStore(store);
}
