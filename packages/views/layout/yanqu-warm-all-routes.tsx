"use client";

// yanqu 本地补丁：进入工作台后，在浏览器空闲时一次性预热所有主要页面
// （路由代码 + 页面骨架 + 列表数据），之后切换页面直接用本地缓存，不再等网络。
// 宁可首次进入多加载几秒，也要后续切换丝滑（机主 2026-11-09 要求）。

import { useEffect } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { useAuthStore } from "@multica/core/auth";
import { paths, useCurrentWorkspace, useWorkspaceSlug } from "@multica/core/paths";
import { chatSessionsOptions } from "@multica/core/chat/queries";
import { inboxListOptions } from "@multica/core/inbox/queries";
import { projectListOptions } from "@multica/core/projects/queries";
import { autopilotListOptions } from "@multica/core/autopilots/queries";
import { runtimeListOptions } from "@multica/core/runtimes/queries";
import {
  agentListOptions,
  memberListOptions,
  skillListOptions,
  squadListOptions,
} from "@multica/core/workspace/queries";
import { useNavigation } from "../navigation";

export function YanquWarmAllRoutes() {
  const qc = useQueryClient();
  const slug = useWorkspaceSlug();
  const wsId = useCurrentWorkspace()?.id;
  const userId = useAuthStore((s) => s.user?.id ?? null);
  const { prefetch } = useNavigation();

  useEffect(() => {
    if (!slug || !wsId) return;
    let cancelled = false;
    const p = paths.workspace(slug);
    const routes = [
      p.issues(),
      p.inbox(),
      p.chat(),
      p.myIssues(),
      p.myAgents(),
      p.agents(),
      p.squads(),
      p.projects(),
      p.autopilots(),
      p.skills(),
      p.runtimes(),
      p.views(),
    ].filter((x): x is string => typeof x === "string");

    const warmData = () => {
      void qc.prefetchQuery(inboxListOptions(wsId));
      void qc.prefetchQuery(chatSessionsOptions(wsId));
      void qc.prefetchQuery(agentListOptions(wsId));
      void qc.prefetchQuery(memberListOptions(wsId));
      void qc.prefetchQuery(squadListOptions(wsId));
      void qc.prefetchQuery(skillListOptions(wsId));
      void qc.prefetchQuery(projectListOptions(wsId));
      void qc.prefetchQuery(autopilotListOptions(wsId));
      void qc.prefetchQuery(runtimeListOptions(wsId));
    };

    // 逐个预热路由，避免一次性并发几十个请求挤占当前页面
    const warmRoutes = async () => {
      for (const r of routes) {
        if (cancelled) return;
        try {
          prefetch?.(r);
        } catch {
          /* 预热失败不影响正常使用 */
        }
        await new Promise((res) => setTimeout(res, 250));
      }
    };

    const start = () => {
      if (cancelled) return;
      warmData();
      void warmRoutes();
    };
    const timer = window.setTimeout(() => {
      if (typeof window.requestIdleCallback === "function") {
        window.requestIdleCallback(start, { timeout: 2000 });
      } else {
        start();
      }
    }, 800);
    return () => {
      cancelled = true;
      window.clearTimeout(timer);
    };
  }, [qc, slug, wsId, userId, prefetch]);

  return null;
}
