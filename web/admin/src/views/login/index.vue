<script setup lang="ts">
import { computed, onMounted, reactive, ref } from "vue";
import { useRouter } from "vue-router";
import { getConfig } from "@/config";
import { message } from "@/utils/message";
import type { FormInstance, FormRules } from "element-plus";
import { useUserStoreHook } from "@/store/modules/user";

defineOptions({
  name: "Login"
});

const router = useRouter();
const loading = ref(false);
const setupMode = ref(false);
const formRef = ref<FormInstance>();
const userStore = useUserStoreHook();
const title = computed(() => getConfig().Title || "Localshare Admin");

const form = reactive({
  password: "",
  confirm: ""
});

const rules = computed<FormRules>(() => {
  const base: FormRules = {
    password: [
      {
        required: true,
        message: setupMode.value ? "请输入初始化密码" : "请输入管理员密码",
        trigger: "blur"
      },
      {
        min: 8,
        message: "密码至少 8 位",
        trigger: "blur"
      }
    ]
  };
  if (setupMode.value) {
    base.confirm = [
      {
        required: true,
        message: "请再次输入密码",
        trigger: "blur"
      },
      {
        validator: (_rule, value, callback) => {
          if (value !== form.password) {
            callback(new Error("两次输入的密码不一致"));
          } else {
            callback();
          }
        },
        trigger: "blur"
      }
    ];
  }
  return base;
});

async function ensureSession() {
  const session = await userStore.ensureSession();
  setupMode.value = session.setup_required;
  if (session.authenticated) {
    await router.replace("/dashboard");
  }
}

async function submit() {
  if (!formRef.value) return;
  await formRef.value.validate(async valid => {
    if (!valid) return;
    loading.value = true;
    try {
      if (setupMode.value) {
        await userStore.setupPassword(form.password);
      }
      await userStore.loginByUsername({ password: form.password });
      message(setupMode.value ? "管理员已初始化" : "登录成功", {
        type: "success"
      });
      await router.replace("/dashboard");
    } catch (error: any) {
      setupMode.value = error?.response?.data?.setup_required ?? setupMode.value;
      message(error?.response?.data?.error ?? "登录失败", { type: "error" });
    } finally {
      loading.value = false;
    }
  });
}

onMounted(() => {
  void ensureSession();
});
</script>

<template>
  <div class="min-h-screen flex items-center justify-center bg-slate-50 px-4">
    <div class="w-full max-w-md">
      <div class="mb-6 text-center">
        <h1 class="text-2xl font-semibold text-slate-900">
          {{ title }}
        </h1>
        <p class="mt-2 text-sm text-slate-500">
          {{ setupMode ? "首次使用请初始化管理员密码" : "使用管理员密码进入后台" }}
        </p>
      </div>

      <el-card shadow="never" class="login-card">
        <el-form
          ref="formRef"
          :model="form"
          :rules="rules"
          label-position="top"
          @submit.prevent
        >
          <el-form-item prop="password" label="密码">
            <el-input
              v-model="form.password"
              show-password
              size="large"
              placeholder="请输入密码"
            />
          </el-form-item>

          <el-form-item v-if="setupMode" prop="confirm" label="确认密码">
            <el-input
              v-model="form.confirm"
              show-password
              size="large"
              placeholder="再次输入密码"
            />
          </el-form-item>

          <el-button
            class="w-full"
            type="primary"
            size="large"
            :loading="loading"
            @click="submit"
          >
            {{ setupMode ? "初始化并进入" : "登录" }}
          </el-button>
        </el-form>
      </el-card>
    </div>
  </div>
</template>

<style scoped>
.login-card {
  border-radius: 8px;
}
</style>
