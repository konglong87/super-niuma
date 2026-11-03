import { useState } from "react";
import type { SkillOverrideRecord, SkillPackageResult, SkillPackageVerifyResult, SkillRecord } from "../lib/types";

// Skill（技能编辑/覆盖/技能包发布与校验）面板状态域（原样迁自 InspectorPanels）。
export function useSkillPanel() {
  const [skills, setSkills] = useState<SkillRecord[]>([]);
  const [tenantSkills, setTenantSkills] = useState<SkillRecord[]>([]);
  const [skillOverrides, setSkillOverrides] = useState<SkillOverrideRecord[]>([]);
  const [skillKey, setSkillKey] = useState("webui-test-skill");
  const [skillName, setSkillName] = useState("WebUI Test Skill");
  const [skillVersion, setSkillVersion] = useState(1);
  const [skillContent, setSkillContent] = useState("---\ndescription: WebUI test skill\n---\nUse this skill when validating WebUI flows.\n");
  const [skillEnabled, setSkillEnabled] = useState(true);
  const [skillOverrideConfig, setSkillOverrideConfig] = useState('{"source":"webui"}');
  const [skillValidation, setSkillValidation] = useState("");
  const [skillPackagePath, setSkillPackagePath] = useState("");
  const [skillPackageFileName, setSkillPackageFileName] = useState("");
  const [skillPackageFileSize, setSkillPackageFileSize] = useState(0);
  const [skillPackageBase64, setSkillPackageBase64] = useState("");
  const [skillPackageSchemaName, setSkillPackageSchemaName] = useState("teach_decision_v1");
  const [skillPackagePreview, setSkillPackagePreview] = useState<SkillPackageResult | null>(null);
  const [skillPackageVerify, setSkillPackageVerify] = useState<SkillPackageVerifyResult | null>(null);

  return {
    skills,
    setSkills,
    tenantSkills,
    setTenantSkills,
    skillOverrides,
    setSkillOverrides,
    skillKey,
    setSkillKey,
    skillName,
    setSkillName,
    skillVersion,
    setSkillVersion,
    skillContent,
    setSkillContent,
    skillEnabled,
    setSkillEnabled,
    skillOverrideConfig,
    setSkillOverrideConfig,
    skillValidation,
    setSkillValidation,
    skillPackagePath,
    setSkillPackagePath,
    skillPackageFileName,
    setSkillPackageFileName,
    skillPackageFileSize,
    setSkillPackageFileSize,
    skillPackageBase64,
    setSkillPackageBase64,
    skillPackageSchemaName,
    setSkillPackageSchemaName,
    skillPackagePreview,
    setSkillPackagePreview,
    skillPackageVerify,
    setSkillPackageVerify,
  };

}
