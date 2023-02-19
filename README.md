# ddpai_downloader

More info at https://www.eionix.co.in/2019/10/10/reverse-engineer-ddpai-firmware.html

# Docker build
```
docker build . -f Dockerfile.multistage -t ddpai-downloader-test
```

# POST commands
http://193.168.0.1/vcam/cmd.cgi?cmd=APP_EventListReq
http://193.168.0.1/G_20230214105816_052_0030.mp4

http://193.168.0.1/vcam/cmd.cgi?cmd=APP_PlaybackListReq
http://193.168.0.1/G_20230214105816_052_0030.mp4

http://193.168.0.1/vcam/cmd.cgi?cmd=APP_GeneralSave {"int_params":[{"key":"osd_speedUnit","value":1}], "string_params":[]}
http://193.168.0.1/vcam/cmd.cgi?cmd=API_GeneralQuery
http://193.168.0.1/vcam/cmd.cgi?cmd=API_TrackInfoQuery
http://193.168.0.1/vcam/cmd.cgi?cmd=API_GetModuleState
http://193.168.0.1/vcam/cmd.cgi?cmd=API_GetMailBoxData
http://193.168.0.1/vcam/cmd.cgi?cmd=API_SetAppLiveState {"switch":"off"}
http://193.168.0.1/vcam/cmd.cgi?cmd=API_GetGpsState
http://193.168.0.1/vcam/cmd.cgi?cmd=API_TrackDivideTime {"divide_time":900}
http://193.168.0.1/vcam/cmd.cgi?cmd=API_GpsFileListReq

/vcam/cmd.cgi?cmd=APP_AvCapSet {"stream_type":0,"frmrate":30}
/vcam/cmd.cgi?cmd=APP_AvCapReq {"errcode":0,"data":{"bs_pixel":"1920x1080","bs_bitrat":10240,"bs_frmrate":30,"ss_pixel":"854x480","ss_bitrat":1536,"ss_frmrate":30,"aud_samplerate":16000,"aud_pt":"AACLC"}}
http://193.168.0.1/vcam/cmd.cgi?cmd=APP_PlaybackLiveSwitch {"switch":"live","playtime":""}
live stream tcp://193.168.0.1:6200/

    API_RequestSessionID
    API_RequestCertificate
    API_SyncDate
    API_GetBaseInfo
    APP_AvCapReq
    APP_AvCapSet
    APP_PlaybackLiveSwitch
    API_GetMailboxData
    API_Logout
    API_TestGsensor
    API_TestSerial
    API_TestIndicator
    APP_PlaybackListReq
    APP_PlaybackPageListReq
    API_GeneralSave
    API_GeneralQuery
    APP_DeleteEvent
    API_CameraCapture
    API_AuthModify
    API_GetStorageInfo
    API_MmcFormat
    API_GetModuleState
    API_SetTimeForUpdateOrderNum
    API_ClearModuleFlags
    APP_AvInit
    APP_EventListReq
    APP_TimeLapseVideoListReq
    API_PlayModeQuery
    API_AuthQuery
    API_Reboot
    API_RestartWifi
    API_UpdFileMd5
    API_SetLogonInfo
    API_GetLogonRecord
    APP_ParkingEventListReq
    APP_ParkingEventListClear
    API_SuperDownload
    API_ButtonMatch
    API_WpsConnect
    API_GetResolution
    API_SetLockFile
    APP_StopDownload
    API_SetRouterAuth
    API_GetRouterStatus
    API_UpdateCamera
    API_GetLegalInfo
    API_GetSdBadClus
    API_SetDefaultCfg
    API_SetUuid
    API_SetSn
    API_GetEachFileSize
    API_BanMaUnbind
    API_BanMaSync
    API_SetApMode
    APP_EquipTestReady
    API_HwinfoQuery
    API_EquipAudioLoop
    API_EquipGetTime
    API_EquipLED
    API_EquipButtonMatch
    API_SetTestResult
    API_EquipGSensor
    API_EquipSpeaker
    API_EquipResetBtn
    API_EquipPhotoBtn
    API_EquipMuteBtn
    API_EquipMuteBtn
    API_EquipResetCfg
    API_EquipLegalSet
    API_EquipGetSensorVer
    API_EquipOpenRtsp
    API_RecordOpt
    API_GetGsensorState
    API_EquipACCState
    API_EquipGetTempetureAndHumidity
    API_EquipGetWiFiStatus
    API_EquipDeleteFacUsbFile
    API_EquipODBTest
    API_SetGsensorValue
    API_SetAudioCapGain
    API_SetBitRate1
    API_SetBitRate2
    API_SetBitRate3
    API_SetBitRate4
    API_SetBitRate5
    API_SetBitRate6
    API_SetBitRate7
    API_SetBitRate8
    API_SetBitRate9
    API_SetBitRate10
    API_SetBitRate11
    API_SetBitRate12
    API_SetBitRate13
    API_SetBitRate14
    API_SetBitRate15
    API_SetBitRate16
    API_SetBitRate17
    API_SetBitRate18
    API_GetAispeechState
    API_SetSpeakRange
    API_SetEmmcMeasure
    API_GetEmmcMeasure
    API_Get_ConnectAccStatus
    API_Get_PageFileListStatus
    API_SetTarCamlog
    API_GetCarCustomVersion
    API_SetPowerOff
