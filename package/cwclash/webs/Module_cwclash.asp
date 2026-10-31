<!doctype html>
<html lang="zh-CN">
<head>
  <meta charset="UTF-8">
  <meta http-equiv="Cache-Control" content="no-store">
  <meta name="viewport" content="width=device-width,initial-scale=1.0">
  <!-- Legacy ASUS menu code uses inline handlers and eval; assets remain local. -->
  <meta http-equiv="Content-Security-Policy" content="default-src 'self'; script-src 'self' 'unsafe-inline' 'unsafe-eval'; style-src 'self' 'unsafe-inline'; connect-src 'self'; img-src 'self' data:; object-src 'none'; base-uri 'self'">
  <meta name="cwclash-model" content="<% nvram_get("productid"); %>">
  <meta name="cwclash-skin" content="<% nvram_get("sc_skin"); %>">
  <link rel="icon" href="/images/favicon.png">
  <link rel="stylesheet" href="/index_style.css">
  <link rel="stylesheet" href="/form_style.css">
  <link rel="stylesheet" href="/res/softcenter.css">
  <link rel="stylesheet" href="/res/cwclash/style.css?v=__CW_VERSION__">
  <script src="/js/jquery.js"></script>
  <script src="/state.js"></script>
  <script src="/popup.js"></script>
  <script src="/help.js"></script>
  <script src="/general.js"></script>
  <script src="/res/softcenter.js"></script>
  <script defer src="/res/cwclash/host.js?v=__CW_VERSION__"></script>
  <title>软件中心 - 长城 Clash</title>
</head>
<body id="app" skin="ASUSWRT">
  <div id="TopBanner"></div>
  <div id="Loading" class="popup_bg"></div>
  <iframe name="hidden_frame" id="hidden_frame" title="固件后台操作" src="about:blank" width="0" height="0" frameborder="0" hidden></iframe>
  <!-- Keep the firmware's compatibility form outside the Vue application. -->
  <form name="form" method="post" action="/apply.cgi" target="hidden_frame" hidden>
    <input type="hidden" name="current_page" value="Module_cwclash.asp">
    <input type="hidden" name="next_page" value="Module_cwclash.asp">
    <input type="hidden" name="modified" value="0">
    <input type="hidden" name="action_mode" value="">
    <input type="hidden" name="action_script" value="">
    <input type="hidden" name="action_wait" value="">
    <input type="hidden" name="preferred_lang" value="<% nvram_get("preferred_lang"); %>">
    <input type="hidden" name="firmver" value="<% nvram_get("firmver"); %>">
  </form>
  <table class="content" align="center" cellpadding="0" cellspacing="0">
    <tr>
      <td width="17"></td>
      <td valign="top" width="202"><div id="mainMenu"></div><div id="subMenu"></div></td>
      <td valign="top">
        <div id="tabMenu" class="submenuBlock"></div>
        <table width="760" cellpadding="5" cellspacing="0" class="FormTitle" id="FormTitle">
          <tr><td valign="top" id="cwclash-panel">
            <div id="cwclash-host-error" role="alert" hidden>固件菜单加载失败。可刷新页面重试，或返回软件中心。</div>
            <div id="cwclash-app"></div>
            <noscript>请启用 JavaScript 后使用长城 Clash。</noscript>
          </td></tr>
        </table>
      </td>
    </tr>
  </table>
  <div id="footer"></div>
  <script type="module" src="/res/cwclash/app.js?v=__CW_VERSION__"></script>
</body>
</html>
