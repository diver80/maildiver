// Marks message document so live Omarchy CSS variables harmonise plain/unstyled mails cleanly.
(function () {
  if (document.documentElement) {
    document.documentElement.dataset.omathunderStyled = "1";
  }
})();
