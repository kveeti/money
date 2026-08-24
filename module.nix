{ moneyPkg }:
{ config, lib, ... }:

let
  cfg = config.services.money;
in
{
  options.services.money = {
    enable = lib.mkEnableOption "Money tracker";

    environment = lib.mkOption {
      type = lib.types.attrsOf lib.types.str;
      default = { };
      description = "Environment variables passed to Money";
    };

    environmentFile = lib.mkOption {
      type = lib.types.nullOr lib.types.path;
      default = null;
      description = "Environment file containing Money configuration and secrets";
    };
  };

  config = lib.mkIf cfg.enable {
    users.groups.money = { };
    users.users.money = {
      isSystemUser = true;
      group = "money";
    };

    systemd.services.money = {
      description = "Money tracker";
      wantedBy = [ "multi-user.target" ];
      wants = [ "network-online.target" ];
      after = [ "network-online.target" ];
      environment = cfg.environment;

      serviceConfig = {
        ExecStart = "${moneyPkg}/bin/money";
        Restart = "always";
        RestartSec = 5;
        TimeoutStopSec = 45;
        User = "money";
        Group = "money";

        StateDirectory = "money";
        WorkingDirectory = "/var/lib/money";
        EnvironmentFile = lib.mkIf (cfg.environmentFile != null) cfg.environmentFile;

        CapabilityBoundingSet = [ "" ];
        DeviceAllow = [
          "/dev/stdin"
          "/dev/urandom"
        ];
        DevicePolicy = "strict";
        LockPersonality = true;
        MemoryDenyWriteExecute = true;
        NoNewPrivileges = true;
        PrivateDevices = true;
        PrivateTmp = true;
        PrivateUsers = true;
        ProcSubset = "pid";
        ProtectClock = true;
        ProtectControlGroups = true;
        ProtectHome = true;
        ProtectHostname = true;
        ProtectKernelLogs = true;
        ProtectKernelModules = true;
        ProtectKernelTunables = true;
        ProtectProc = "invisible";
        ProtectSystem = "strict";
        RemoveIPC = true;
        RestrictAddressFamilies = [
          "AF_INET"
          "AF_INET6"
          "AF_UNIX"
        ];
        RestrictNamespaces = true;
        RestrictRealtime = true;
        RestrictSUIDSGID = true;
        SystemCallArchitectures = "native";
        SystemCallFilter = [
          "@system-service"
          "~@privileged"
          "~@resources"
        ];
        UMask = "0027";
      };
    };
  };
}
