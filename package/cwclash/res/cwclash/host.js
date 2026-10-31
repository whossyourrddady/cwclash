// SPDX-License-Identifier: GPL-3.0-or-later
// Use the firmware's real menu, footer and skin, without another app window.
(function () {
  'use strict';
  function initialize() {
    var skin = document.querySelector('meta[name="cwclash-skin"]');
    var name = skin && skin.getAttribute('content');
    document.body.setAttribute('skin', /^(ASUSWRT|ROG|TUF|TS)$/.test(name || '') ? name : 'ASUSWRT');
    try {
      if (typeof window.show_menu !== 'function') throw new Error('Firmware menu unavailable');
      window.menu_hook = function () {
        if (!Array.isArray(window.tabtitle) || !Array.isArray(window.tablink)) return;
        window.tabtitle[window.tabtitle.length - 1] = ['', '软件中心', '长城 Clash'];
        window.tablink[window.tablink.length - 1] = ['', 'Module_Softcenter.asp', 'Module_cwclash.asp'];
      };
      window.show_menu(window.menu_hook);
    } catch (error) {
      document.getElementById('cwclash-host-error').hidden = false;
      console.warn('cwclash firmware menu:', error);
    }
  }
  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', initialize, { once: true });
  else initialize();
})();
